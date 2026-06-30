package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type cachedMeta struct {
	model    string
	did      string
	protocol string
}

type deviceStatus struct {
	Name       string                 `json:"name"`
	Host       string                 `json:"host"`
	Online     bool                   `json:"online"`
	Power      string                 `json:"power"`
	Brightness *int                   `json:"brightness,omitempty"`
	ColorTemp  *int                   `json:"color_temp,omitempty"`
	Mode       *int                   `json:"mode,omitempty"`
	Model      string                 `json:"model"`
	Protocol   string                 `json:"protocol"`
	Props      map[string]interface{} `json:"props,omitempty"`
	Error      string                 `json:"error,omitempty"`
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
						status := pollDeviceFullState(&e)
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

func pollDeviceFullState(entry *DeviceEntry) *deviceStatus {
	status := &deviceStatus{
		Name: entry.Name,
		Host: entry.Host,
	}

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
		pi, err := getInfo(dev)
		if err != nil {
			status.Error = err.Error()
			return status
		}
		model = pi.model
		did = pi.did
		proto = detectProtocolQuick(dev, model, did)

		globalCache.mu.Lock()
		globalCache.metadata[entry.Name] = cachedMeta{model: model, did: did, protocol: proto}
		globalCache.mu.Unlock()
	}

	status.Online = true
	status.Model = model
	status.Protocol = proto

	if proto == "miot" {
		fillMiotState(dev, status, model, did)
	} else if proto == "miio" {
		fillMiioState(dev, status, model)
	}

	return status
}

func fillMiotState(dev *Device, status *deviceStatus, model, did string) {
	spec, err := getSpec(model, false)
	if err != nil {
		status.Error = fmt.Sprintf("no spec: %v", err)
		return
	}

	var params []map[string]int
	for _, svc := range spec.Services {
		for _, prop := range svc.Properties {
			if prop.IsReadable() {
				params = append(params, map[string]int{"siid": svc.IID, "piid": prop.IID})
			}
		}
	}
	if len(params) == 0 {
		params = []map[string]int{{"siid": 2, "piid": 1}}
	}

	results, batchErr := miotGetPropertiesBatched(dev, did, params, 15)

	status.Power = "UNKNOWN"
	if batchErr == nil {
		status.Props = make(map[string]interface{})
		for _, r := range results {
			siid, _ := r["siid"].(float64)
			piid, _ := r["piid"].(float64)
			code, _ := r["code"].(float64)
			if code != 0 {
				continue
			}
			val := r["value"]
			name := findPropName(spec, int(siid), int(piid))
			if name == "" {
				name = fmt.Sprintf("%.0f.%.0f", siid, piid)
			}
			key := fmt.Sprintf("%d.%d", int(siid), int(piid))
			status.Props[key] = val

			switch name {
			case "on", "power", "switch":
				if s := formatPower(val); s != "" {
					status.Power = s
				}
			case "brightness", "bright", "Brightness":
				if n, ok := toInt(val); ok {
					status.Brightness = &n
					if status.Power == "UNKNOWN" && n > 0 {
						status.Power = "ON"
					}
				}
			case "color-temperature", "color_temp", "colour_temperature", "ColorTemperature", "cct", "ct":
				if n, ok := toInt(val); ok {
					status.ColorTemp = &n
				}
			case "snm", "mode", "Scenes", "scene_num", "color_mode":
				if n, ok := toInt(val); ok {
					status.Mode = &n
				}
			}
		}
	}

	if status.Power == "UNKNOWN" {
		status.Power = readPowerFallback(dev, spec, did)
	}
	if status.Power == "UNKNOWN" {
		result, miioErr := miioGetProp(dev, "power")
		if miioErr == nil && len(result) > 0 {
			if s, ok := result[0].(string); ok {
				status.Power = strings.ToUpper(s)
			}
		}
	}
	if status.Power == "" {
		status.Power = "UNKNOWN"
	}

	if batchErr != nil {
		status.Error = fmt.Sprintf("read failed: %v", batchErr)
	}
}

func readPowerFallback(dev *Device, spec *MiotSpec, did string) string {
	r, err := miotGetProperties(dev, did, []map[string]int{{"siid": 2, "piid": 1}})
	if err == nil && len(r) > 0 {
		if code, ok := r[0]["code"].(float64); ok && code == 0 {
			return formatPower(r[0]["value"])
		}
	}
	for _, svc := range spec.Services {
		for _, prop := range svc.Properties {
			if !prop.IsReadable() {
				continue
			}
			name := prop.Type
			if idx := strings.LastIndex(name, ":"); idx >= 0 {
				name = name[idx+1:]
			}
			switch name {
			case "on", "power", "switch":
				r, err := miotGetProperties(dev, did, []map[string]int{{"siid": svc.IID, "piid": prop.IID}})
				if err == nil && len(r) > 0 {
					if code, ok := r[0]["code"].(float64); ok && code == 0 {
						return formatPower(r[0]["value"])
					}
				}
			}
		}
	}
	return ""
}

func formatPower(val interface{}) string {
	switch v := val.(type) {
	case bool:
		if v {
			return "ON"
		}
		return "OFF"
	case string:
		s := strings.ToLower(v)
		if s == "on" || s == "true" || s == "1" {
			return "ON"
		}
		return "OFF"
	case float64:
		if v != 0 {
			return "ON"
		}
		return "OFF"
	}
	return ""
}

func fillMiioState(dev *Device, status *deviceStatus, model string) {
	m := lookupMiioModel(model)
	if m == nil {
		result, err := miioGetProp(dev, "power")
		if err == nil && len(result) > 0 {
			s, _ := result[0].(string)
			status.Power = strings.ToUpper(s)
		}
		return
	}

	props := m.AllProps()
	if len(props) == 0 {
		return
	}

	vals, err := miioGetProps(dev, props)
	if err != nil {
		status.Error = err.Error()
		return
	}

	status.Props = make(map[string]interface{})
	for i, p := range props {
		if i < len(vals) {
			status.Props[p] = vals[i]
		}
	}

	for i, p := range props {
		if i >= len(vals) {
			continue
		}
		v := vals[i]

		switch p {
		case m.Power:
			if s, ok := v.(string); ok {
				status.Power = strings.ToUpper(s)
				if status.Power == "" {
					status.Power = "UNKNOWN"
				}
			} else if b, ok := v.(bool); ok {
				if b {
					status.Power = "ON"
				} else {
					status.Power = "OFF"
				}
			}
		}
		if m.Bright != "" && p == m.Bright {
			if n, ok := toInt(v); ok {
				status.Brightness = &n
			}
		}
		if m.CT != "" && p == m.CT {
			if n, ok := toInt(v); ok {
				status.ColorTemp = &n
			}
		}
		if m.Mode != "" && p == m.Mode {
			if n, ok := toInt(v); ok {
				status.Mode = &n
			}
		}
	}

	if status.Power == "" {
		status.Power = "UNKNOWN"
	}
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case string:
		if n == "on" || n == "true" {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func refreshDeviceCache(entry *DeviceEntry) *deviceStatus {
	updated := pollDeviceFullState(entry)
	globalCache.mu.Lock()
	globalCache.statuses[entry.Name] = updated
	globalCache.mu.Unlock()
	return updated
}

func StartAutomationEngine() {
	go func() {
		var lastProcessedMinute string
		for {
			now := time.Now()
			currentMin := now.Format("15:04")
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
	path := fmt.Sprintf("%s/automation.csv", dataDir())
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
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

	var records [][]string
	if r := csv.NewReader(f); r != nil {
		r.Comment = '#'
		r.FieldsPerRecord = -1
		records, err = r.ReadAll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Automation] Error reading CSV: %v\n", err)
			return
		}
	}

	for i, rec := range records {
		if i == 0 && len(rec) > 0 && rec[0] == "time" {
			continue
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
	case "brightness":
		exitCode = cmdBrightness(dev, valStr)
	case "mode":
		exitCode = cmdMode(dev, valStr)
	case "colortemp":
		exitCode = cmdColortemp(dev, valStr)
	default:
		if valStr != "" {
			exitCode = cmdSetProp(dev, command, valStr, command)
		} else {
			fmt.Fprintf(os.Stderr, "[Automation] Unsupported schedule command: %s\n", command)
			return
		}
	}

	if exitCode != 0 {
		fmt.Fprintf(os.Stderr, "[Automation] Command failed for '%s'\n", deviceName)
	} else {
		fmt.Printf("[Automation] Command successful for '%s'\n", deviceName)
		go refreshDeviceCache(entry)
	}
}
