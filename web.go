package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"net"
)

type deviceStatus struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Online   bool   `json:"online"`
	Power    string `json:"power"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
	Error    string `json:"error,omitempty"`
}

// cachedMeta remembers structural data so we don't spam miIO.info over UDP
type cachedMeta struct {
	model    string
	did      string
	protocol string
}

type ServerCache struct {
	mu       sync.RWMutex
	statuses map[string]*deviceStatus
	metadata map[string]cachedMeta
}

var globalCache = &ServerCache{
	statuses: make(map[string]*deviceStatus),
	metadata: make(map[string]cachedMeta),
}

// StartBackgroundPoller updates statuses asynchronously every 5 seconds
func StartBackgroundPoller() {
	go func() {
		for {
			entries, err := LoadDevices()
			if err == nil {
				var wg sync.WaitGroup
				for _, entry := range entries {
					wg.Add(1)
					go func(e DeviceEntry) {
						defer wg.Done()
						status := fetchDeviceStatusQuick(&e)
						globalCache.mu.Lock()
						globalCache.statuses[e.Name] = status
						globalCache.mu.Unlock()
					}(entry)
				}
				wg.Wait()
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

// StartAutomationEngine checks the automation.csv file every 10 seconds and triggers actions
func StartAutomationEngine() {
	go func() {
		var lastProcessedMinute string
		for {
			now := time.Now()
			currentMin := now.Format("15:04") // Format: "HH:MM"
			currentDate := now.Format("2006-01-02")
			timeKey := currentMin + "@" + currentDate

			if timeKey != lastProcessedMinute {
				runScheduledTasks(currentMin)
				lastProcessedMinute = timeKey
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func runScheduledTasks(currentTime string) {
	path := filepath.Join(dataDir(), "automation.csv")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Write a default template so users have a structured starting point
			_ = os.MkdirAll(dataDir(), 0755)
			defaultCSV := "time,device,command,value\n" +
				"# Format: time(HH:MM), deviceName, command, optionalValue\n" +
				"# Examples:\n" +
				"# 07:30,lamp,on,\n" +
				"# 18:00,lamp,brightness,80\n" +
				"# 23:00,lamp,off,\n"
			_ = os.WriteFile(path, []byte(defaultCSV), 0644)
		}
		return
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comment = '#' // Enable comment parsing support
	r.FieldsPerRecord = -1

	records, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Automation] Error reading CSV: %v\n", err)
		return
	}

	for i, rec := range records {
		if i == 0 && len(rec) > 0 && rec[0] == "time" {
			continue // Skip CSV header
		}
		if len(rec) < 3 {
			continue
		}
		taskTime := strings.TrimSpace(rec[0])
		deviceName := strings.TrimSpace(rec[1])
		command := strings.TrimSpace(rec[2])
		var valStr string
		if len(rec) >= 4 {
			valStr = strings.TrimSpace(rec[3])
		}

		if taskTime == currentTime {
			fmt.Printf("[Automation] [%s] Triggering %s for device: %s (value: %s)\n", taskTime, command, deviceName, valStr)
			executeScheduledTask(deviceName, command, valStr)
		}
	}
}

func executeScheduledTask(deviceName, command, valStr string) {
	entry, err := FindDevice(deviceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Automation] Error: Device '%s' not found: %v\n", deviceName, err)
		return
	}

	dev, err := newDevice(entry.Host, entry.Token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Automation] Error initializing '%s': %v\n", deviceName, err)
		return
	}

	var exitCode int
	switch command {
	case "on":
		exitCode = cmdOn(dev)
	case "off":
		exitCode = cmdOff(dev)
	case "brightness", "mode", "colortemp":
		exitCode = cmdSetProp(dev, command, valStr, command)
	default:
		fmt.Fprintf(os.Stderr, "[Automation] Unsupported schedule command: %s\n", command)
		return
	}

	if exitCode != 0 {
		fmt.Fprintf(os.Stderr, "[Automation] Command failed for '%s'\n", deviceName)
	} else {
		fmt.Printf("[Automation] Command successful for '%s'\n", deviceName)
		// Instantly update dynamic state cache
		go func() {
			updated := fetchDeviceStatusQuick(entry)
			globalCache.mu.Lock()
			globalCache.statuses[entry.Name] = updated
			globalCache.mu.Unlock()
		}()
	}
}

func fetchDeviceStatusQuick(entry *DeviceEntry) *deviceStatus {
	status := &deviceStatus{Name: entry.Name, Host: entry.Host, Power: "UNKNOWN"}

	dev, err := newDevice(entry.Host, entry.Token)
	if err != nil {
		status.Error = err.Error()
		return status
	}

	globalCache.mu.RLock()
	meta, hasMeta := globalCache.metadata[entry.Name]
	globalCache.mu.RUnlock()

	var model, did, proto string

	if hasMeta {
		model = meta.model
		did = meta.did
		proto = meta.protocol
	} else {
		// Only perform heavy discovery protocol setup once per lifespan or until success
		pi, err := getInfo(dev)
		if err != nil {
			status.Error = err.Error()
			return status
		}
		model = pi.model
		did = pi.did
		proto = detectProtocol(dev, model, did)

		globalCache.mu.Lock()
		globalCache.metadata[entry.Name] = cachedMeta{model: model, did: did, protocol: proto}
		globalCache.mu.Unlock()
	}

	status.Online = true
	status.Model = model
	status.Protocol = proto

	// Fast track property evaluation (Mimicking native fast CLI routines)
	if proto == "miot" {
		result, err := miotGetProperties(dev, did, []map[string]int{{"siid": 2, "piid": 1}})
		if err == nil && len(result) > 0 {
			if code, ok := result[0]["code"].(float64); ok && code == 0 {
				if val, ok := result[0]["value"].(bool); ok {
					if val {
						status.Power = "ON"
					} else {
						status.Power = "OFF"
					}
				}
			}
		}
	} else if proto == "miio" {
		result, err := miioGetProp(dev, "power")
		if err == nil && len(result) > 0 {
			if s, ok := result[0].(string); ok {
				status.Power = strings.ToUpper(s)
			}
		}
	}

	return status
}

func apiDevices(w http.ResponseWriter, r *http.Request) {
	entries, err := LoadDevices()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	globalCache.mu.RLock()
	defer globalCache.mu.RUnlock()

	statuses := make([]*deviceStatus, 0)
	for _, entry := range entries {
		if cached, ok := globalCache.statuses[entry.Name]; ok {
			statuses = append(statuses, cached)
		} else {
			// Fallback placeholder if background thread hasn't finished first sweep
			statuses = append(statuses, &deviceStatus{Name: entry.Name, Host: entry.Host, Power: "LOADING..."})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}

func apiDeviceAction(name, action string, w http.ResponseWriter, r *http.Request) {
	entry, err := FindDevice(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	dev, err := newDevice(entry.Host, entry.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var exitCode int
	switch action {
	case "on":
		exitCode = cmdOn(dev)
	case "off":
		exitCode = cmdOff(dev)
	case "brightness", "mode", "colortemp":
		var body struct {
			Value interface{} `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		strVal := fmt.Sprint(body.Value)
		exitCode = cmdSetProp(dev, action, strVal, action)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	if exitCode != 0 {
		http.Error(w, "failed to execute action: "+action, http.StatusInternalServerError)
		return
	}

	// Trigger immediate state recalculation dynamically for fast UI interaction feedback
	go func() {
		updated := fetchDeviceStatusQuick(entry)
		globalCache.mu.Lock()
		globalCache.statuses[entry.Name] = updated
		globalCache.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

var dashboardTmpl = template.Must(template.New("dashboard").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Dashboard</title>
<style>
  body { font-family: sans-serif; background: #121214; color: #e1e1e6; padding: 20px; margin: 0; }
  h1 { font-size: 1.4rem; color: #9b67ef; margin-top: 25px; }
  h1:first-of-type { margin-top: 0; }
  .grid { display: grid; gap: 12px; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); margin-top: 15px; }
  .card { background: #202024; padding: 15px; border-radius: 8px; border: 1px solid #323238; }
  .meta { font-size: 0.8rem; color: #7c7c8a; margin: 4px 0 10px 0; }
  .power { font-weight: bold; font-size: 1.1rem; margin-bottom: 12px; }
  .ON { color: #04d361; } .OFF { color: #f75a68; }
  .ctrl { display: flex; gap: 6px; margin-bottom: 10px; }
  button { background: #4e4e54; color: #fff; border: 0; padding: 6px 12px; border-radius: 4px; cursor: pointer; font-size: 0.85rem; }
  button:hover { background: #62626a; }
  .row { display: flex; align-items: center; gap: 8px; margin-top: 6px; font-size: 0.8rem; color: #8d8d99; }
  input { flex: 1; accent-color: #9b67ef; }
  .err { color: #f75a68; font-size: 0.8rem; margin-top: 5px; }
  textarea { width: 100%; height: 180px; font-family: monospace; background: #121214; color: #e1e1e6; border: 1px solid #323238; border-radius: 4px; padding: 10px; box-sizing: border-box; resize: vertical; margin-bottom: 12px; }
</style>
</head>
<body>
  <h1>Devices</h1>
  <div class="grid" id="devs">Loading...</div>

  <h1>Automation Schedule</h1>
  <div class="card" style="margin-top: 15px; max-width: 650px;">
    <div class="meta">Manage periodic events (Format: time,device,command,value). Lines starting with # are ignored.</div>
    <textarea id="csvText" placeholder="time,device,command,value"></textarea>
    <div style="display: flex; gap: 10px; align-items: center;">
      <button onclick="saveSchedule()" id="saveBtn">Save Schedule</button>
      <button onclick="loadSchedule()">Reload</button>
      <span id="statusMsg" style="font-size: 0.85rem;"></span>
    </div>
  </div>

<script>
function esc(s) {
  if (!s) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
}

async function refresh() {
  try {
    const res = await fetch('/api/devices');
    const data = await res.json();
    
    var htmlContent = '';
    for (var i = 0; i < data.length; i++) {
      var d = data[i];
      var opacity = d.online ? '1' : '0.5';
      var modelStr = d.model ? d.model : '?';
      var protoStr = d.protocol ? d.protocol : '?';
      var powerStr = d.power ? d.power : 'UNKNOWN';
      
      htmlContent += '<div class="card" style="opacity: ' + opacity + '">';
      htmlContent += '  <strong style="font-size:1rem">' + esc(d.name) + '</strong>';
      htmlContent += '  <div class="meta">' + esc(d.host) + ' &bull; ' + esc(modelStr) + ' &bull; ' + esc(protoStr) + '</div>';
      htmlContent += '  <div class="power ' + esc(powerStr) + '">' + esc(powerStr) + '</div>';
      htmlContent += '  <div class="ctrl">';
      htmlContent += '    <button onclick="act(\'' + esc(d.name) + '\',\'on\')">ON</button>';
      htmlContent += '    <button onclick="act(\'' + esc(d.name) + '\',\'off\')">OFF</button>';
      htmlContent += '  </div>';
      
      if (d.online) {
        htmlContent += '  <div class="row"><label>Bright</label><input type="range" min="1" max="100" onchange="act(\'' + esc(d.name) + '\',\'brightness\',this.value)"></div>';
        htmlContent += '  <div class="row"><label>Mode</label><input type="range" min="0" max="3" onchange="act(\'' + esc(d.name) + '\',\'mode\',this.value)"></div>';
        htmlContent += '  <div class="row"><label>ColorK</label><input type="range" min="2700" max="6500" step="100" onchange="act(\'' + esc(d.name) + '\',\'colortemp\',this.value)"></div>';
      }
      
      if (d.error) {
        htmlContent += '  <div class="err">' + esc(d.error) + '</div>';
      }
      
      htmlContent += '</div>';
    }
    
    document.getElementById('devs').innerHTML = htmlContent;
  } catch(e) {}
}

async function act(name, cmd, val) {
  const opts = { method: 'POST' };
  if(val !== undefined) {
    opts.headers = {'Content-Type': 'application/json'};
    opts.body = JSON.stringify({ value: window.isNaN(val) ? val : parseInt(val) });
  }
  await fetch('/api/devices/' + encodeURIComponent(name) + '/' + cmd, opts);
  setTimeout(refresh, 200);
}

async function loadSchedule() {
  try {
    const res = await fetch('/api/automation');
    const txt = await res.text();
    document.getElementById('csvText').value = txt;
  } catch(e) {}
}

async function saveSchedule() {
  const btn = document.getElementById('saveBtn');
  const msg = document.getElementById('statusMsg');
  btn.disabled = true;
  msg.textContent = 'Saving...';
  msg.style.color = '#7c7c8a';
  try {
    const csv = document.getElementById('csvText').value;
    const res = await fetch('/api/automation', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({ csv: csv })
    });
    if (res.ok) {
      msg.textContent = 'Saved successfully!';
      msg.style.color = '#04d361';
    } else {
      msg.textContent = 'Failed to save!';
      msg.style.color = '#f75a68';
    }
  } catch(e) {
    msg.textContent = 'Error occurred!';
    msg.style.color = '#f75a68';
  }
  btn.disabled = false;
  setTimeout(() => { msg.textContent = ''; }, 3000);
}

refresh();
loadSchedule();
setInterval(refresh, 4000);
</script>
</body>
</html>`))

func serveWeb(listenAddr string) error {
	// Initialize and run background optimization and automation loops
	StartBackgroundPoller()
	StartAutomationEngine()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			apiDevices(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/devices/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/devices/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		apiDeviceAction(parts[0], parts[1], w, r)
	})

	mux.HandleFunc("/api/automation", func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dataDir(), "automation.csv")
		if r.Method == "GET" {
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					defaultCSV := "time,device,command,value\n" +
						"# Format: time(HH:MM), deviceName, command, optionalValue\n" +
						"# Examples:\n" +
						"# 07:30,lamp,on,\n" +
						"# 18:00,lamp,brightness,80\n" +
						"# 23:00,lamp,off,\n"
					_ = os.MkdirAll(dataDir(), 0755)
					_ = os.WriteFile(path, []byte(defaultCSV), 0644)
					w.Header().Set("Content-Type", "text/plain")
					w.Write([]byte(defaultCSV))
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Write(data)
		} else if r.Method == "POST" {
			var body struct {
				CSV string `json:"csv"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if err := os.WriteFile(path, []byte(body.CSV), 0644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if webPath := os.Getenv("MIIOT_CLI_WEB"); webPath != "" {
			http.ServeFile(w, r, webPath)
			return
		}
		dashboardTmpl.Execute(w, nil)
	})

	// Parse the network type and address
	var network, address string
	if strings.HasPrefix(listenAddr, "unix://") {
		network = "unix"
		address = strings.TrimPrefix(listenAddr, "unix://")

		// Remove any trailing colon configurations (like :544) if accidental,
		// since unix sockets use file paths, not ports.
		if idx := strings.Index(address, ":"); idx != -1 {
			// Optional: keeping only the path portion if your setup appends a port
			address = address[:idx]
		}

		// Clean up existing socket file if it wasn't gracefully closed last time
		_ = os.Remove(address)
	} else {
		network = "tcp"
		address = listenAddr
		// Strip http:// prefix if a user accidentally passes it
		address = strings.TrimPrefix(address, "http://")
	}

	// Create our abstract network listener
	listener, err := net.Listen(network, address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s (%s): %w", network, address, err)
	}
	defer listener.Close()

	// If using unix socket, ensure proper file permissions so web servers/proxies can access it
	if network == "unix" {
		_ = os.Chmod(address, 0666)
	}

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	fmt.Printf("[Server] Listening on %s://%s\n", network, address)
	return server.Serve(listener)
}
