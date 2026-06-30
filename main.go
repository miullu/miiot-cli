package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func miotGetProperties(dev *Device, did string, params []map[string]int) ([]map[string]interface{}, error) {
	var apiParams []map[string]interface{}
	for _, p := range params {
		apiParams = append(apiParams, map[string]interface{}{
			"did":  did,
			"siid": p["siid"],
			"piid": p["piid"],
		})
	}
	raw, err := dev.Send("get_properties", apiParams)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []map[string]interface{} `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("device error: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func miotSetProperty(dev *Device, did string, siid, piid int, value interface{}) ([]map[string]interface{}, error) {
	params := []map[string]interface{}{
		{"did": did, "siid": siid, "piid": piid, "value": value},
	}
	raw, err := dev.Send("set_properties", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []map[string]interface{} `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("device error: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func miioGetProp(dev *Device, prop string) ([]interface{}, error) {
	raw, err := dev.Send("get_prop", []string{prop})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []interface{} `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("device error: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func miioSetPower(dev *Device, state string) ([]interface{}, error) {
	raw, err := dev.Send("set_power", []string{state})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []interface{} `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("device error: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func newDevice(host, token string) (*Device, error) {
	return NewDevice(host, token, 5*time.Second)
}

func detectProtocol(dev *Device, model, did string) string {
	protos := loadProtocols()
	if p, ok := protos[model]; ok {
		return p
	}

	fmt.Fprintf(os.Stderr, "Detecting protocol for %s...\n", model)

	result, err := miotGetProperties(dev, did, []map[string]int{{"siid": 2, "piid": 1}})
	if err == nil {
		for _, item := range result {
			if code, ok := item["code"]; ok {
				if c, ok := code.(float64); ok && c == 0 {
					saveProtocol(model, "miot")
					return "miot"
				}
			}
		}
	}

	result, err = miotGetProperties(dev, did, []map[string]int{{"siid": 3, "piid": 1}})
	if err == nil {
		for _, item := range result {
			if code, ok := item["code"]; ok {
				if c, ok := code.(float64); ok && c == 0 {
					saveProtocol(model, "miot")
					return "miot"
				}
			}
		}
	}

	miioResult, miioErr := miioGetProp(dev, "power")
	if miioErr == nil && miioResult != nil {
		saveProtocol(model, "miio")
		return "miio"
	}

	return "unknown"
}

type parsedInfo struct {
	model string
	did   string
}

func getInfo(dev *Device) (*parsedInfo, error) {
	info, wrongToken, err := dev.Info()
	if err != nil {
		return nil, err
	}
	if wrongToken || len(info) == 0 {
		return nil, fmt.Errorf("wrong token")
	}
	model, _ := info["model"].(string)
	if model == "" {
		model = "unknown"
	}
	did := ""
	if v, ok := info["did"]; ok {
		did = fmt.Sprint(v)
	}
	if did == "" {
		did = fmt.Sprint(dev.deviceID)
	}
	return &parsedInfo{model: model, did: did}, nil
}

func cmdInfo(dev *Device) int {
	info, wrongToken, err := dev.Info()
	if err != nil {
		fmt.Println("Device offline")
		return 1
	}
	if wrongToken || len(info) == 0 {
		fmt.Println("Device responded but token is wrong")
		return 1
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	fmt.Println(string(data))
	return 0
}

func cmdDetect(dev *Device) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	proto := detectProtocol(dev, pi.model, pi.did)
	fmt.Printf("Model:    %s\n", pi.model)
	fmt.Printf("DID:      %s\n", pi.did)
	fmt.Printf("Protocol: %s\n", proto)
	return 0
}

func cmdOn(dev *Device) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	proto := detectProtocol(dev, pi.model, pi.did)

	if proto == "miot" {
		spec, err := getSpec(pi.model, false)
		if err == nil {
			if siid, piid, ok := findLightOnProp(spec); ok {
				res, err := miotSetProperty(dev, pi.did, siid, piid, true)
				if err == nil && len(res) > 0 {
					if code, ok := res[0]["code"].(float64); ok && code == 0 {
						fmt.Println("Turned ON via MIoT")
						return 0
					}
				}
			}
		}
		res, err := miotSetProperty(dev, pi.did, 2, 1, true)
		if err == nil && len(res) > 0 {
			if code, ok := res[0]["code"].(float64); ok && code == 0 {
				fmt.Println("Turned ON via MIoT (default siid/piid)")
				return 0
			}
		}
	} else if proto == "miio" {
		_, err := miioSetPower(dev, "on")
		if err == nil {
			fmt.Println("Turned ON via miIO")
			return 0
		}
	}

	fmt.Println("Failed to turn on")
	return 1
}

func cmdOff(dev *Device) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	proto := detectProtocol(dev, pi.model, pi.did)

	if proto == "miot" {
		spec, err := getSpec(pi.model, false)
		if err == nil {
			if siid, piid, ok := findLightOnProp(spec); ok {
				res, err := miotSetProperty(dev, pi.did, siid, piid, false)
				if err == nil && len(res) > 0 {
					if code, ok := res[0]["code"].(float64); ok && code == 0 {
						fmt.Println("Turned OFF via MIoT")
						return 0
					}
				}
			}
		}
		res, err := miotSetProperty(dev, pi.did, 2, 1, false)
		if err == nil && len(res) > 0 {
			if code, ok := res[0]["code"].(float64); ok && code == 0 {
				fmt.Println("Turned OFF via MIoT (default siid/piid)")
				return 0
			}
		}
	} else if proto == "miio" {
		_, err := miioSetPower(dev, "off")
		if err == nil {
			fmt.Println("Turned OFF via miIO")
			return 0
		}
	}

	fmt.Println("Failed to turn off")
	return 1
}

func cmdStatus(dev *Device) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	proto := detectProtocol(dev, pi.model, pi.did)

	fmt.Printf("Model:    %s\n", pi.model)
	fmt.Printf("DID:      %s\n", pi.did)
	fmt.Printf("Protocol: %s\n", proto)

	if proto == "miot" {
		result, err := miotGetProperties(dev, pi.did, []map[string]int{{"siid": 2, "piid": 1}})
		if err == nil && len(result) > 0 {
			if code, ok := result[0]["code"].(float64); ok && code == 0 {
				val, _ := result[0]["value"].(bool)
				if val {
					fmt.Println("Power:    ON")
				} else {
					fmt.Println("Power:    OFF")
				}
				return 0
			}
		}
		fmt.Println("Power:    unknown (MIoT read failed)")
	} else if proto == "miio" {
		result, err := miioGetProp(dev, "power")
		if err == nil && len(result) > 0 {
			s, _ := result[0].(string)
			fmt.Printf("Power:    %s\n", strings.ToUpper(s))
			return 0
		}
		fmt.Println("Power:    unknown (miIO read failed)")
	}
	return 1
}

func cmdSpec(dev *Device, force bool) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	fmt.Printf("Model: %s\n", pi.model)

	spec, err := getSpec(pi.model, force)
	if err != nil {
		fmt.Printf("No MIoT spec found for this model: %v\n", err)
		return 1
	}
	path, _ := specCachePath(pi.model)
	fmt.Printf("Spec cached at: %s\n", path)
	fmt.Printf("Services: %d\n", len(spec.Services))

	for _, svc := range spec.Services {
		short := svc.Type
		if idx := strings.LastIndex(svc.Type, ":"); idx >= 0 {
			parts := strings.Split(svc.Type, ":")
			if len(parts) >= 2 {
				short = parts[len(parts)-2]
			}
		}
		var propNames []string
		for _, p := range svc.Properties {
			pn := p.Type
			if idx := strings.LastIndex(p.Type, ":"); idx >= 0 {
				pn = p.Type[idx+1:]
			}
			propNames = append(propNames, pn)
		}
		fmt.Printf("  siid=%d %s: %s\n", svc.IID, short, strings.Join(propNames, ", "))
	}
	return 0
}

func cmdGet(dev *Device, propName string) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	proto := detectProtocol(dev, pi.model, pi.did)

	if proto == "miot" {
		spec, err := getSpec(pi.model, false)
		if err != nil {
			fmt.Printf("Failed to get spec: %v\n", err)
			return 1
		}
		siid, piid, ok := findPropByName(spec, propName)
		if !ok {
			fmt.Printf("Unknown property: %s\n", propName)
			return 1
		}
		result, err := miotGetProperties(dev, pi.did, []map[string]int{{"siid": siid, "piid": piid}})
		if err == nil && len(result) > 0 {
			if code, ok := result[0]["code"].(float64); ok && code == 0 {
				fmt.Printf("%s: %v\n", propName, result[0]["value"])
				return 0
			}
		}
	} else if proto == "miio" {
		result, err := miioGetProp(dev, propName)
		if err == nil && len(result) > 0 {
			fmt.Printf("%s: %v\n", propName, result[0])
			return 0
		}
	}

	fmt.Printf("Failed to read %s\n", propName)
	return 1
}

func cmdSetProp(dev *Device, propName, rawValue, displayName string) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}

	var value interface{}
	if v, err := strconv.Atoi(rawValue); err == nil {
		value = v
	} else if v, err := strconv.ParseFloat(rawValue, 64); err == nil {
		value = v
	} else {
		low := strings.ToLower(rawValue)
		switch low {
		case "true", "on":
			value = true
		case "false", "off":
			value = false
		default:
			value = rawValue
		}
	}

	proto := detectProtocol(dev, pi.model, pi.did)

	if proto == "miot" {
		spec, err := getSpec(pi.model, false)
		if err != nil {
			fmt.Printf("Failed to get spec: %v\n", err)
			return 1
		}
		siid, piid, ok := findPropByName(spec, propName)
		if !ok {
			fmt.Printf("Unknown property: %s\n", propName)
			return 1
		}
		_, err = miotSetProperty(dev, pi.did, siid, piid, value)
		if err == nil {
			fmt.Printf("%s set to %v\n", displayName, value)
			return 0
		}
	} else if proto == "miio" && propName == "power" {
		s := "off"
		if b, ok := value.(bool); ok && b {
			s = "on"
		}
		if v, ok := value.(int); ok && v == 1 {
			s = "on"
		}
		if str, ok := value.(string); ok && (str == "on" || str == "true") {
			s = "on"
		}
		_, err := miioSetPower(dev, s)
		if err == nil {
			fmt.Printf("Power set to %s\n", s)
			return 0
		}
	} else if proto == "miio" {
		raw, err := dev.Send("set_prop", []interface{}{propName, value})
		if err == nil {
			var resp struct {
				Result []interface{} `json:"result"`
			}
			if json.Unmarshal(raw, &resp) == nil {
				fmt.Printf("%s set to %v\n", displayName, value)
				return 0
			}
		}
	}

	fmt.Printf("Failed to set %s\n", displayName)
	return 1
}

func cmdSet(dev *Device, propName, rawValue string) int {
	return cmdSetProp(dev, propName, rawValue, propName)
}

func cmdBrightness(dev *Device, rawValue string) int {
	return cmdSetProp(dev, "brightness", rawValue, "brightness")
}

func cmdMode(dev *Device, rawValue string) int {
	return cmdSetProp(dev, "mode", rawValue, "mode")
}

func cmdColortemp(dev *Device, rawValue string) int {
	return cmdSetProp(dev, "colortemp", rawValue, "colortemp")
}

func runWithDevice(host, token string, args []string, force bool) int {
	dev, err := newDevice(host, token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	command := "status"
	if len(args) > 0 {
		command = args[0]
	}

	needsValue := map[string]int{
		"get": 1, "set": 2,
		"brightness": 1, "mode": 1, "colortemp": 1,
	}

	if minArgs, needs := needsValue[command]; needs {
		if len(args) < minArgs+1 {
			switch command {
			case "get":
				fmt.Fprintln(os.Stderr, "Error: get requires a property name")
			case "set":
				fmt.Fprintln(os.Stderr, "Error: set requires a property name and value")
			default:
				fmt.Fprintf(os.Stderr, "Error: %s requires a value\n", command)
			}
			return 1
		}
	}

	switch command {
	case "info", "on", "off", "status", "detect", "spec", "get", "set",
		"brightness", "mode", "colortemp":
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		return 1
	}

	switch command {
	case "info":
		return cmdInfo(dev)
	case "detect":
		return cmdDetect(dev)
	case "on":
		return cmdOn(dev)
	case "off":
		return cmdOff(dev)
	case "status":
		return cmdStatus(dev)
	case "spec":
		return cmdSpec(dev, force)
	case "get":
		return cmdGet(dev, args[1])
	case "set":
		return cmdSet(dev, args[1], args[2])
	case "brightness":
		return cmdBrightness(dev, args[1])
	case "mode":
		return cmdMode(dev, args[1])
	case "colortemp":
		return cmdColortemp(dev, args[1])
	}
	return 0
}

func usage() {
	fmt.Fprintf(os.Stderr, `miiot-cli: Control Xiaomi MIoT/miIO devices locally.

Usage:
  miiot-cli list                          - List stored devices
  miiot-cli add <name> <host> <token>     - Add a device
  miiot-cli remove <name>                 - Remove a device
  miiot-cli <name> info                   - Get device info
  miiot-cli <name> on                     - Turn light on
  miiot-cli <name> off                    - Turn light off
  miiot-cli <name> status                 - Get power status
  miiot-cli <name> detect                 - Detect MIoT vs miIO protocol
  miiot-cli <name> spec                   - Fetch and cache MIoT spec
  miiot-cli <name> get <prop>             - Read a property (e.g. power,brightness)
  miiot-cli <name> set <prop> <value>     - Set a property
  miiot-cli <name> brightness <val>       - Set brightness (0-100)
  miiot-cli <name> mode <val>             - Set mode (e.g. 0=day,1=night)
  miiot-cli <name> colortemp <val>        - Set color temperature (kelvin)
  miiot-cli serve [--listen <addr+port>]  - Start web dashboard (default :8080)

Legacy (direct IP/token, no CSV):
  miiot-cli <host> <token> <command> [args...]

Environment:
  MIIOT_CLI_PATH    Data directory (devices.csv, spec cache, etc.)
                    Default: ~/.cache/miiot-cli
  MIIOT_CLI_WEB     Path to custom HTML dashboard file (served instead of built-in)
`)
}

func cmdList() int {
	devices, err := LoadDevices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading devices:", err)
		return 1
	}
	if len(devices) == 0 {
		fmt.Println("No devices stored.")
		fmt.Println("Use: miiot-cli add <name> <host> <token>")
		return 0
	}
	fmt.Printf("%-20s %-21s %-32s\n", "Name", "IP", "Token")
	fmt.Println(strings.Repeat("-", 75))
	for _, d := range devices {
		tok := d.Token
		if len(tok) > 8 {
			tok = tok[:4] + "..." + tok[len(tok)-4:]
		}
		fmt.Printf("%-20s %-21s %-32s\n", d.Name, d.Host, tok)
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	force := false
	var cleanArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "--force" {
			force = true
		} else {
			cleanArgs = append(cleanArgs, arg)
		}
	}

	first := cleanArgs[0]

	switch first {
	case "help", "--help", "-h":
		usage()
		return

	case "list":
		os.Exit(cmdList())

	case "add":
		if len(cleanArgs) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: miiot-cli add <name> <host> <token>")
			os.Exit(1)
		}
		if err := AddDevice(cleanArgs[1], cleanArgs[2], cleanArgs[3]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		fmt.Printf("Device '%s' added.\n", cleanArgs[1])

	case "remove":
		if len(cleanArgs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: miiot-cli remove <name>")
			os.Exit(1)
		}
		if err := RemoveDevice(cleanArgs[1]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		fmt.Printf("Device '%s' removed.\n", cleanArgs[1])

	case "serve":
		port := ":8080"
		for i, arg := range cleanArgs {
			if arg == "--listen" && i+1 < len(cleanArgs) {
				port = ":" + cleanArgs[i+1]
			}
		}
		fmt.Printf("Starting web server on %s\n", port)
		if err := serveWeb(port); err != nil {
			fmt.Fprintln(os.Stderr, "Server error:", err)
			os.Exit(1)
		}

	default:
		if entry, err := FindDevice(first); err == nil {
			os.Exit(runWithDevice(entry.Host, entry.Token, cleanArgs[1:], force))
			return
		}

		if len(cleanArgs) >= 2 {
			os.Exit(runWithDevice(cleanArgs[0], cleanArgs[1], cleanArgs[2:], force))
			return
		}

		fmt.Fprintf(os.Stderr, "Unknown command or device: %s\n", first)
		usage()
		os.Exit(1)
	}
}
