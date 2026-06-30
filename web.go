package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
			statuses = append(statuses, &deviceStatus{
				Name: entry.Name, Host: entry.Host, Power: "LOADING...",
			})
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

	go refreshDeviceCache(entry)

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
  .grid { display: grid; gap: 12px; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); margin-top: 15px; }
  .card { background: #202024; padding: 15px; border-radius: 8px; border: 1px solid #323238; }
  .meta { font-size: 0.8rem; color: #7c7c8a; margin: 4px 0 10px 0; }
  .power { font-weight: bold; font-size: 1.1rem; margin-bottom: 12px; }
  .ON { color: #04d361; } .OFF { color: #f75a68; }
  .ctrl { display: flex; gap: 6px; margin-bottom: 10px; }
  button { background: #4e4e54; color: #fff; border: 0; padding: 6px 12px; border-radius: 4px; cursor: pointer; font-size: 0.85rem; }
  button:hover { background: #62626a; }
  .row { display: flex; align-items: center; gap: 8px; margin-top: 6px; font-size: 0.8rem; color: #8d8d99; }
  .row label { min-width: 60px; }
  .row .val { min-width: 40px; text-align: right; color: #e1e1e6; }
  input[type=range] { flex: 1; accent-color: #9b67ef; }
  .err { color: #f75a68; font-size: 0.8rem; margin-top: 5px; }
  .raw-toggle { font-size: 0.75rem; color: #7c7c8a; cursor: pointer; margin-top: 6px; }
  .raw-toggle:hover { color: #9b67ef; }
  .raw-json { display: none; font-size: 0.7rem; color: #7c7c8a; margin-top: 6px; background: #121214; padding: 8px; border-radius: 4px; max-height: 200px; overflow: auto; white-space: pre-wrap; }
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

function toggleRaw(id) {
  var el = document.getElementById(id);
  if (el) el.style.display = el.style.display === 'block' ? 'none' : 'block';
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

      var briVal = d.brightness !== undefined && d.brightness !== null ? d.brightness : '';
      var ctVal = d.color_temp !== undefined && d.color_temp !== null ? d.color_temp : '';
      var modeVal = d.mode !== undefined && d.mode !== null ? d.mode : '';

      htmlContent += '<div class="card" style="opacity: ' + opacity + '">';
      htmlContent += '  <strong style="font-size:1rem">' + esc(d.name) + '</strong>';
      htmlContent += '  <div class="meta">' + esc(d.host) + ' &bull; ' + esc(modelStr) + ' &bull; ' + esc(protoStr) + '</div>';
      htmlContent += '  <div class="power ' + esc(d.power) + '">' + esc(d.power) + '</div>';
      htmlContent += '  <div class="ctrl">';
      htmlContent += '    <button onclick="act(\'' + esc(d.name) + '\',\'on\')">ON</button>';
      htmlContent += '    <button onclick="act(\'' + esc(d.name) + '\',\'off\')">OFF</button>';
      htmlContent += '  </div>';

      if (d.online) {
        htmlContent += '  <div class="row"><label>Bright</label><input type="range" min="1" max="100" value="' + esc(briVal) + '" onchange="act(\'' + esc(d.name) + '\',\'brightness\',this.value)"><span class="val">' + esc(briVal) + '</span></div>';
        htmlContent += '  <div class="row"><label>Mode</label><input type="range" min="0" max="6" value="' + esc(modeVal) + '" onchange="act(\'' + esc(d.name) + '\',\'mode\',this.value)"><span class="val">' + esc(modeVal) + '</span></div>';
        htmlContent += '  <div class="row"><label>ColorK</label><input type="range" min="2700" max="6500" step="100" value="' + esc(ctVal) + '" onchange="act(\'' + esc(d.name) + '\',\'colortemp\',this.value)"><span class="val">' + esc(ctVal) + '</span></div>';
        htmlContent += '  <div class="raw-toggle" onclick="toggleRaw(\'raw-' + i + '\')">&#9654; Raw properties</div>';
        htmlContent += '  <div class="raw-json" id="raw-' + i + '">' + esc(JSON.stringify(d.props, null, 2)) + '</div>';
      }

      if (d.error) {
        htmlContent += '  <div class="err">' + esc(d.error) + '</div>';
      }

      htmlContent += '</div>';
    }

    document.getElementById('devs').innerHTML = htmlContent;
  } catch(e) {
    console.error('refresh error:', e);
  }
}

async function act(name, cmd, val) {
  const opts = { method: 'POST' };
  if(val !== undefined) {
    opts.headers = {'Content-Type': 'application/json'};
    var numVal = parseInt(val);
    opts.body = JSON.stringify({ value: isNaN(numVal) ? val : numVal });
  }
  await fetch('/api/devices/' + encodeURIComponent(name) + '/' + cmd, opts);
  setTimeout(refresh, 300);
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

	var network, address string
	if strings.HasPrefix(listenAddr, "unix://") {
		network = "unix"
		address = strings.TrimPrefix(listenAddr, "unix://")
		if idx := strings.Index(address, ":"); idx != -1 {
			address = address[:idx]
		}
		_ = os.Remove(address)
	} else {
		network = "tcp"
		address = listenAddr
		address = strings.TrimPrefix(address, "http://")
	}

	listener, err := net.Listen(network, address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s (%s): %w", network, address, err)
	}
	defer listener.Close()

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
