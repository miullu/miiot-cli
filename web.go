package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
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

func getDeviceStatus(entry *DeviceEntry) *deviceStatus {
	status := &deviceStatus{Name: entry.Name, Host: entry.Host}

	dev, err := newDevice(entry.Host, entry.Token)
	if err != nil {
		status.Error = err.Error()
		return status
	}

	pi, err := getInfo(dev)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Online = true
	status.Model = pi.model

	proto := detectProtocol(dev, pi.model, pi.did)
	status.Protocol = proto

	if proto == "miot" {
		result, err := miotGetProperties(dev, pi.did, []map[string]int{{"siid": 2, "piid": 1}})
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

	if status.Power == "" {
		status.Power = "UNKNOWN"
	}

	return status
}

func apiDevices(w http.ResponseWriter, r *http.Request) {
	entries, err := LoadDevices()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	statuses := make([]*deviceStatus, 0)
	for _, entry := range entries {
		statuses = append(statuses, getDeviceStatus(&entry))
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

	var result string
	switch action {
	case "on":
		exitCode := cmdOn(dev)
		if exitCode == 0 {
			result = "ok"
		} else {
			http.Error(w, "failed to turn on", http.StatusInternalServerError)
			return
		}
	case "off":
		exitCode := cmdOff(dev)
		if exitCode == 0 {
			result = "ok"
		} else {
			http.Error(w, "failed to turn off", http.StatusInternalServerError)
			return
		}
	case "brightness":
		var body struct {
			Value int `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		exitCode := cmdBrightness(dev, fmt.Sprint(body.Value))
		if exitCode == 0 {
			result = "ok"
		} else {
			http.Error(w, "failed to set brightness", http.StatusInternalServerError)
			return
		}
	case "set":
		var body struct {
			Property string      `json:"property"`
			Value    interface{} `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		strVal := fmt.Sprint(body.Value)
		exitCode := cmdSetProp(dev, body.Property, strVal, body.Property)
		if exitCode == 0 {
			result = "ok"
		} else {
			http.Error(w, "failed to set property", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": result})
}

var dashboardTmpl = template.Must(template.New("dashboard").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>miiot-cli Dashboard</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #1a1a2e; color: #e0e0e0; padding: 20px;
  }
  h1 { font-size: 1.5rem; margin-bottom: 20px; color: #b388ff; }
  .devices { display: grid; gap: 16px; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); }
  .card {
    background: #16213e; border-radius: 12px; padding: 20px;
    border: 1px solid #2a2a4a; transition: border-color .2s;
  }
  .card.online { border-color: #4caf50; }
  .card.offline { border-color: #f44336; opacity: .6; }
  .card h2 { font-size: 1.1rem; margin-bottom: 8px; }
  .card .meta { font-size: .85rem; color: #999; margin-bottom: 12px; }
  .card .meta span { margin-right: 12px; }
  .card .power { font-weight: bold; font-size: 1.2rem; margin-bottom: 12px; }
  .power.on { color: #4caf50; }
  .power.off { color: #f44336; }
  .power.unknown { color: #ff9800; }
  .actions { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  button {
    padding: 8px 20px; border: none; border-radius: 6px; cursor: pointer;
    font-size: .9rem; font-weight: 600; transition: opacity .2s;
  }
  button:hover { opacity: .8; }
  .btn-on { background: #4caf50; color: #fff; }
  .btn-off { background: #f44336; color: #fff; }
  .btn-on:disabled, .btn-off:disabled { opacity: .4; cursor: not-allowed; }
  .slider-group { display: flex; align-items: center; gap: 8px; margin-top: 8px; }
  .slider-group label { font-size: .85rem; color: #999; }
  input[type=range] { flex: 1; accent-color: #b388ff; }
  .slider-value { font-size: .85rem; color: #b388ff; min-width: 30px; text-align: right; }
  .loading { text-align: center; padding: 40px; color: #666; }
  .error { color: #f44336; font-size: .85rem; margin-top: 4px; }
  .footer { margin-top: 24px; text-align: center; font-size: .8rem; color: #555; }
  .footer a { color: #b388ff; text-decoration: none; }
</style>
</head>
<body>
<h1>miiot-cli Dashboard</h1>
<div class="devices" id="devices"><div class="loading">Loading...</div></div>
<div class="footer">miiot-cli &mdash; <a href="#" onclick="refresh()">refresh</a></div>
<script>
const $ = s => document.querySelector(s);
const $$ = s => document.querySelectorAll(s);

async function refresh() {
  const container = document.getElementById('devices');
  try {
    const res = await fetch('/api/devices');
    const devices = await res.json();
    if (!devices.length) {
      container.innerHTML = '<div class="card"><h2>No devices</h2><p>Add devices via CLI: miiot-cli add &lt;name&gt; &lt;host&gt; &lt;token&gt;</p></div>';
      return;
    }
    container.innerHTML = devices.map(d => renderCard(d)).join('');
  } catch (e) {
    container.innerHTML = '<div class="card error">Failed to load devices</div>';
  }
}

function renderCard(d) {
  const cls = d.online ? 'online' : 'offline';
  const powerCls = (d.power || '').toLowerCase();
  return '<div class="card ' + cls + '">' +
    '<h2>' + esc(d.name) + '</h2>' +
    '<div class="meta"><span>' + esc(d.host) + '</span><span>' + esc(d.model || '?') + '</span><span>' + esc(d.protocol || '?') + '</span></div>' +
    '<div class="power ' + powerCls + '">' + esc(d.power || 'UNKNOWN') + '</div>' +
    '<div class="actions">' +
      '<button class="btn-on" onclick="action(\'' + d.name + '\',\'on\')" ' + (!d.online ? 'disabled' : '') + '>ON</button>' +
      '<button class="btn-off" onclick="action(\'' + d.name + '\',\'off\')" ' + (!d.online ? 'disabled' : '') + '>OFF</button>' +
    '</div>' +
    (d.online ? '<div class="slider-group"><label>Brightness</label><input type="range" min="0" max="100" value="50" oninput="this.nextElementSibling.textContent=this.value" onchange="action(\'' + d.name + '\',\'brightness\',this.value)"><span class="slider-value">50</span></div>' : '') +
    (d.error ? '<div class="error">' + esc(d.error) + '</div>' : '') +
  '</div>';
}

async function action(name, cmd, value) {
  const url = '/api/devices/' + encodeURIComponent(name) + '/' + cmd;
  const opts = { method: 'POST' };
  if (value !== undefined) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify({ value: parseInt(value) });
  }
  try {
    await fetch(url, opts);
    setTimeout(refresh, 500);
  } catch (e) {}
}

function esc(s) {
  if (!s) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>`))

func serveWeb(port string) error {
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
		name := parts[0]
		action := parts[1]

		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		apiDeviceAction(name, action, w, r)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		dashboardTmpl.Execute(w, nil)
	})

	server := &http.Server{
		Addr:         port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server.ListenAndServe()
}
