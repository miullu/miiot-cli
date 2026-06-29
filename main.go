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

func cmdSet(dev *Device, propName, rawValue string) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}

	var value interface{}
	if v, err := strconv.Atoi(rawValue); err == nil {
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
			fmt.Printf("%s set to %v\n", propName, value)
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
				fmt.Printf("%s set to %v\n", propName, value)
				return 0
			}
		}
	}

	fmt.Printf("Failed to set %s\n", propName)
	return 1
}

func usage() {
	fmt.Fprintf(os.Stderr, `miiot-cli: Control Xiaomi MIoT/miIO devices locally.

Usage:
  miiot-cli <host> <token> info        - Get device info
  miiot-cli <host> <token> on          - Turn light on
  miiot-cli <host> <token> off         - Turn light off
  miiot-cli <host> <token> status      - Get power status
  miiot-cli <host> <token> detect      - Detect MIoT vs miIO protocol
  miiot-cli <host> <token> spec        - Fetch and cache MIoT spec
  miiot-cli <host> <token> get <prop>  - Read a property (e.g. power,brightness)
  miiot-cli <host> <token> set <prop> <value>  - Set a property
`)
}

func main() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}

	host := os.Args[1]
	token := os.Args[2]

	command := "status"
	if len(os.Args) >= 4 {
		command = os.Args[3]
	}

	force := false
	for i, arg := range os.Args {
		if arg == "--force" {
			force = true
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			break
		}
	}

	validCmds := map[string]bool{
		"info": true, "on": true, "off": true, "status": true,
		"detect": true, "spec": true, "get": true, "set": true,
	}
	if !validCmds[command] {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		usage()
		os.Exit(1)
	}

	if command == "get" && len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "Error: get requires a property name")
		usage()
		os.Exit(1)
	}
	if command == "set" && len(os.Args) < 6 {
		fmt.Fprintln(os.Stderr, "Error: set requires a property name and value")
		usage()
		os.Exit(1)
	}

	dev, err := NewDevice(host, token, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	var exitCode int
	switch command {
	case "info":
		exitCode = cmdInfo(dev)
	case "detect":
		exitCode = cmdDetect(dev)
	case "on":
		exitCode = cmdOn(dev)
	case "off":
		exitCode = cmdOff(dev)
	case "status":
		exitCode = cmdStatus(dev)
	case "spec":
		exitCode = cmdSpec(dev, force)
	case "get":
		exitCode = cmdGet(dev, os.Args[4])
	case "set":
		exitCode = cmdSet(dev, os.Args[4], os.Args[5])
	}
	os.Exit(exitCode)
}
