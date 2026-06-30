package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultTimeout = 5 * time.Second

var infoWriter = os.Stderr

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
	proto := detectProtocolQuick(dev, pi.model, pi.did)

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
		m := lookupMiioModel(pi.model)
		actualName := propName
		if m != nil {
			if n, _, ok := m.ResolveProp(propName); ok {
				actualName = n
			}
		}
		result, err := miioGetProp(dev, actualName)
		if err == nil && len(result) > 0 {
			fmt.Printf("%s (%s): %v\n", propName, actualName, result[0])
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

	value := autoDetectValue(rawValue)
	proto := detectProtocolQuick(dev, pi.model, pi.did)

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
	} else if proto == "miio" {
		if propName == "power" {
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
		} else {
			if m := lookupMiioModel(pi.model); m != nil {
				if err := miioSetPropertyMapped(dev, pi.model, propName, value); err == nil {
					fmt.Printf("%s set to %v\n", displayName, value)
					return 0
				}
			}
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

func cmdMiotGet(dev *Device, args []string) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	spec, err := getSpec(pi.model, false)
	if err != nil {
		fmt.Printf("No spec for this model: %v\n", err)
		return 1
	}
	var params []map[string]int
	var names []string
	for _, arg := range args {
		parts := strings.SplitN(arg, ".", 2)
		if len(parts) != 2 {
			fmt.Printf("Invalid format: %s (expected siid.piid)\n", arg)
			return 1
		}
		siid, _ := strconv.Atoi(parts[0])
		piid, _ := strconv.Atoi(parts[1])
		params = append(params, map[string]int{"siid": siid, "piid": piid})
		names = append(names, arg)
	}
	results, err := miotGetPropertiesBatched(dev, pi.did, params, 15)
	if err != nil {
		fmt.Printf("Failed to read properties: %v\n", err)
		return 1
	}
	for i, r := range results {
		name := names[i]
		if i < len(results) {
			code, _ := r["code"].(float64)
			val := r["value"]
			if label := findPropName(spec, params[i]["siid"], params[i]["piid"]); label != "" {
				name = fmt.Sprintf("%s (%s)", label, name)
			}
			if code == 0 {
				fmt.Printf("  %s: %v\n", name, val)
			} else {
				fmt.Printf("  %s: error code=%.0f\n", name, code)
			}
		}
	}
	return 0
}

func cmdMiotSet(dev *Device, args []string) int {
	if len(args) < 2 {
		fmt.Println("Usage: miot-set <siid.piid> <value>")
		return 1
	}
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	parts := strings.SplitN(args[0], ".", 2)
	if len(parts) != 2 {
		fmt.Printf("Invalid format: %s (expected siid.piid)\n", args[0])
		return 1
	}
	siid, _ := strconv.Atoi(parts[0])
	piid, _ := strconv.Atoi(parts[1])
	value := autoDetectValue(args[1])

	_, err = miotSetProperty(dev, pi.did, siid, piid, value)
	if err != nil {
		fmt.Printf("Failed to set: %v\n", err)
		return 1
	}
	fmt.Printf("%s.%d set to %v\n", args[0], piid, value)
	return 0
}

func cmdMiioGet(dev *Device, args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: miio-get <prop> [prop...]")
		return 1
	}
	vals, err := miioGetProps(dev, args)
	if err != nil {
		fmt.Printf("Failed to read: %v\n", err)
		return 1
	}
	for i, p := range args {
		if i < len(vals) {
			fmt.Printf("  %s: %v\n", p, vals[i])
		} else {
			fmt.Printf("  %s: (no response)\n", p)
		}
	}
	return 0
}

func cmdMiioSet(dev *Device, args []string) int {
	if len(args) < 2 {
		fmt.Println("Usage: miio-set <prop> <value>")
		return 1
	}
	prop := args[0]
	value := autoDetectValue(strings.Join(args[1:], " "))

	raw, err := dev.Send("set_prop", []interface{}{prop, value})
	if err != nil {
		fmt.Printf("Failed to set: %v\n", err)
		return 1
	}
	var resp struct {
		Result []interface{} `json:"result"`
	}
	if json.Unmarshal(raw, &resp) == nil {
		fmt.Printf("%s set to %v\n", prop, value)
		return 0
	}
	fmt.Printf("%s set to %v (raw response: %s)\n", prop, value, string(raw))
	return 0
}

func cmdSend(dev *Device, args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: send <method> [params...]")
		return 1
	}
	method := args[0]
	var params interface{}
	if len(args) > 1 {
		list := make([]interface{}, len(args)-1)
		for i, arg := range args[1:] {
			list[i] = autoDetectValue(arg)
		}
		params = list
	} else {
		params = []interface{}{}
	}

	raw, err := miioSendRaw(dev, method, params)
	if err != nil {
		fmt.Printf("Failed: %v\n", err)
		return 1
	}
	var pretty interface{}
	if json.Unmarshal(raw, &pretty) == nil {
		data, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Println(string(raw))
	}
	return 0
}

func cmdProps(dev *Device) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	proto := detectProtocolQuick(dev, pi.model, pi.did)
	fmt.Printf("Model:    %s\n", pi.model)
	fmt.Printf("DID:      %s\n", pi.did)
	fmt.Printf("Protocol: %s\n", proto)
	fmt.Println()

	if proto == "miot" {
		spec, err := getSpec(pi.model, false)
		if err != nil {
			fmt.Printf("No spec available: %v\n", err)
			return 1
		}
		var params []map[string]int
		var labels []string
		for _, svc := range spec.Services {
			for _, prop := range svc.Properties {
				params = append(params, map[string]int{"siid": svc.IID, "piid": prop.IID})
				pn := prop.Type
				if idx := strings.LastIndex(prop.Type, ":"); idx >= 0 {
					pn = prop.Type[idx+1:]
				}
				labels = append(labels, fmt.Sprintf("%d.%d %s", svc.IID, prop.IID, pn))
			}
		}
		if len(params) == 0 {
			fmt.Println("No readable properties in spec")
			return 0
		}
		results, err := miotGetPropertiesBatched(dev, pi.did, params, 15)
		if err != nil {
			fmt.Printf("Read failed: %v\n", err)
			return 1
		}
		for i, r := range results {
			label := labels[i]
			if i < len(results) {
				code, _ := r["code"].(float64)
				if code == 0 {
					fmt.Printf("  %s = %v\n", label, r["value"])
				} else {
					fmt.Printf("  %s = (code %.0f)\n", label, code)
				}
			}
		}
	} else if proto == "miio" {
		m := lookupMiioModel(pi.model)
		if m == nil {
			// Probe common properties
			common := []string{"power", "bright", "brightness", "cct", "color_temp", "snm", "mode"}
			vals, err := miioGetProps(dev, common)
			if err != nil {
				fmt.Printf("Failed to probe: %v\n", err)
				return 1
			}
			for i, p := range common {
				if i < len(vals) && vals[i] != nil {
					fmt.Printf("  %s = %v\n", p, vals[i])
				}
			}
		} else {
			props := m.AllProps()
			if len(props) == 0 {
				fmt.Println("No known properties for this model")
				return 0
			}
			vals, err := miioGetProps(dev, props)
			if err != nil {
				fmt.Printf("Failed to read: %v\n", err)
				return 1
			}
			for i, p := range props {
				if i < len(vals) {
					fmt.Printf("  %s = %v\n", p, vals[i])
				}
			}
		}
	} else {
		fmt.Println("Unknown protocol, cannot read properties")
		return 1
	}
	return 0
}

func cmdDiscovery(dev *Device) int {
	pi, err := getInfo(dev)
	if err != nil {
		fmt.Println("Failed to get device info:", err)
		return 1
	}
	proto := detectProtocol(dev, pi.model, pi.did)
	fmt.Printf("Model:       %s\n", pi.model)
	fmt.Printf("DID:         %s\n", pi.did)
	fmt.Printf("Protocol:    %s\n", proto)

	if proto == "miot" {
		spec, err := getSpec(pi.model, true)
		if err != nil {
			fmt.Printf("Failed to fetch spec: %v\n", err)
		} else {
			fmt.Printf("Spec:        %d services\n", len(spec.Services))
		}
	}

	// Probe all known property names to discover what the device supports
	fmt.Println("\nProbing miIO properties...")
	probeNames := []string{
		"power", "pow", "on",
		"bright", "bri", "brightness", "Brightness",
		"cct", "ct", "color_temp", "color-temperature", "ColorTemperature", "colour_temperature",
		"snm", "mode", "Scenes", "scene_num", "color_mode",
		"color", "rgb", "hue", "sat",
		"sta", "state",
	}
	vals, err := miioGetProps(dev, probeNames)
	if err != nil {
		fmt.Println("No miIO properties responded")
	} else {
		for i, p := range probeNames {
			if i < len(vals) && vals[i] != nil {
				switch v := vals[i].(type) {
				case string:
					if v != "" {
						fmt.Printf("  %s = %q\n", p, v)
					}
				case float64:
					fmt.Printf("  %s = %v\n", p, v)
				case bool:
					fmt.Printf("  %s = %v\n", p, v)
				default:
					if v != nil {
						fmt.Printf("  %s = %v\n", p, v)
					}
				}
			}
		}
	}
	return 0
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

	switch command {
	case "info":
		return cmdInfo(dev)
	case "detect":
		return cmdDetect(dev)
	case "discover":
		return cmdDiscovery(dev)
	case "on":
		return cmdOn(dev)
	case "off":
		return cmdOff(dev)
	case "status":
		return cmdStatus(dev)
	case "spec":
		return cmdSpec(dev, force)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: get requires a property name")
			return 1
		}
		return cmdGet(dev, args[1])
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Error: set requires a property name and value")
			return 1
		}
		return cmdSet(dev, args[1], args[2])
	case "brightness":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: brightness requires a value")
			return 1
		}
		return cmdBrightness(dev, args[1])
	case "mode":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: mode requires a value")
			return 1
		}
		return cmdMode(dev, args[1])
	case "colortemp":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: colortemp requires a value")
			return 1
		}
		return cmdColortemp(dev, args[1])
	case "miot-get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: miot-get requires at least one siid.piid")
			return 1
		}
		return cmdMiotGet(dev, args[1:])
	case "miot-set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Error: miot-set requires siid.piid and value")
			return 1
		}
		return cmdMiotSet(dev, args[1:])
	case "miio-get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: miio-get requires at least one property name")
			return 1
		}
		return cmdMiioGet(dev, args[1:])
	case "miio-set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Error: miio-set requires a property name and value")
			return 1
		}
		return cmdMiioSet(dev, args[1:])
	case "send":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: send requires a method name")
			return 1
		}
		return cmdSend(dev, args[1:])
	case "props":
		return cmdProps(dev)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		return 1
	}
}
