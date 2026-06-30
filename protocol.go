package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type parsedInfo struct {
	model string
	did   string
}

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

func miotGetPropertiesBatched(dev *Device, did string, params []map[string]int, chunkSize int) ([]map[string]interface{}, error) {
	if chunkSize <= 0 {
		chunkSize = 15
	}
	var results []map[string]interface{}
	for i := 0; i < len(params); i += chunkSize {
		end := i + chunkSize
		if end > len(params) {
			end = len(params)
		}
		chunk, err := miotGetProperties(dev, did, params[i:end])
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i/chunkSize, err)
		}
		results = append(results, chunk...)
	}
	return results, nil
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

func miioGetProps(dev *Device, props []string) ([]interface{}, error) {
	params := make([]interface{}, len(props))
	for i, p := range props {
		params[i] = p
	}
	raw, err := dev.Send("get_prop", params)
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

func miioSendRaw(dev *Device, method string, params interface{}) (json.RawMessage, error) {
	return dev.Send(method, params)
}

func newDevice(host, token string) (*Device, error) {
	return NewDevice(host, token, defaultTimeout)
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

func detectProtocol(dev *Device, model, did string) string {
	protos := loadProtocols()
	if p, ok := protos[model]; ok {
		return p
	}

	fmt.Fprintf(infoWriter, "Detecting protocol for %s...\n", model)

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

func detectProtocolQuick(dev *Device, model, did string) string {
	protos := loadProtocols()
	if p, ok := protos[model]; ok {
		return p
	}
	return detectProtocol(dev, model, did)
}

func autoDetectValue(arg string) interface{} {
	if arg == "" {
		return arg
	}
	if arg[0] == '[' || arg[0] == '{' {
		var v interface{}
		if json.Unmarshal([]byte(arg), &v) == nil {
			return v
		}
	}
	// Try integer first
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err == nil && !strings.Contains(arg, ".") {
		return n
	}
	// Then float
	var f float64
	if _, err := fmt.Sscanf(arg, "%f", &f); err == nil {
		return f
	}
	switch strings.ToLower(arg) {
	case "true":
		return true
	case "false":
		return false
	}
	return arg
}

type MiioModelMap struct {
	Power        string
	PowerSetter  string
	Bright       string
	BrightSetter string
	CT           string
	CTSetter     string
	Mode         string
	ModeSetter   string
	ExtraRead    []string
}

var miioModelMaps = map[string]MiioModelMap{
	"philips.light.bulb": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "cct", CTSetter: "set_cct",
		Mode: "snm", ModeSetter: "apply_fixed_scene",
	},
	"philips.light.cbulb": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "cct", CTSetter: "set_cct",
		Mode: "snm", ModeSetter: "apply_fixed_scene",
	},
	"philips.light.downlight": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "cct", CTSetter: "set_cct",
		Mode: "snm", ModeSetter: "apply_fixed_scene",
	},
	"philips.light.bceiling1": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "cct", CTSetter: "set_cct",
		Mode: "snm", ModeSetter: "apply_fixed_scene",
	},
	"philips.light.moonlight": {
		Power: "pow", PowerSetter: "set_power",
		Bright: "bri", BrightSetter: "set_bright",
		Mode: "snm", ModeSetter: "apply_fixed_scene",
	},
	"philips.light.sread1": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
	},
	"philips.light.sread2": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		Mode: "scene_num",
	},
	"opple.light.bydceiling": {
		Power: "state", PowerSetter: "SetState",
		Bright: "Brightness", BrightSetter: "SetBrightness",
		CT: "ColorTemperature", CTSetter: "SetColorTemperature",
		Mode: "Scenes", ModeSetter: "SetScenes",
	},
	"opple.light.fanlight": {
		Power: "LightPower", PowerSetter: "SetLightPower",
		Bright: "Brightness", BrightSetter: "SetBrightness",
		CT: "ColorTemperature", CTSetter: "SetColorTemperature",
		Mode: "Scenes", ModeSetter: "SetScenes",
	},
	"yeelink.light.color1": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "ct", CTSetter: "set_ct_abx",
		ExtraRead: []string{"color_mode", "hue", "sat", "rgb"},
	},
	"yeelink.light.mono1": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
	},
	"yeelink.light.strip1": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "ct", CTSetter: "set_ct_abx",
	},
	"yeelink.light.lamp1": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "ct", CTSetter: "set_ct_abx",
	},
	"yeelink.light.ceiling1": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "ct", CTSetter: "set_ct_abx",
	},
	"yeelink.light.ceiling2": {
		Power: "power", PowerSetter: "set_power",
		Bright: "bright", BrightSetter: "set_bright",
		CT: "ct", CTSetter: "set_ct_abx",
	},
	"chuangmi.plug.hmi205": {
		Power: "power", PowerSetter: "set_power",
	},
	"chuangmi.plug.m1": {
		Power: "power", PowerSetter: "set_power",
	},
	"chuangmi.plug.v1": {
		Power: "on", PowerSetter: "",
		ExtraRead: []string{"temperature", "wifi_led"},
	},
	"chuangmi.plug.v3": {
		Power: "on", PowerSetter: "set_power",
		ExtraRead: []string{"temperature", "wifi_led", "usb_on"},
	},
}

func lookupMiioModel(model string) *MiioModelMap {
	if m, ok := miioModelMaps[model]; ok {
		return &m
	}
	for key, m := range miioModelMaps {
		if strings.HasPrefix(model, key) || strings.HasPrefix(key, model) {
			return &m
		}
		if strings.HasSuffix(model, "."+key) || strings.HasSuffix(key, "."+model) {
			return &m
		}
	}
	return nil
}

func (m *MiioModelMap) ResolveProp(highLevel string) (miioName, setter string, ok bool) {
	switch highLevel {
	case "power":
		return m.Power, m.PowerSetter, m.Power != ""
	case "brightness":
		return m.Bright, m.BrightSetter, m.Bright != ""
	case "colortemp", "color_temp", "color-temperature", "colour_temperature":
		return m.CT, m.CTSetter, m.CT != ""
	case "mode":
		return m.Mode, m.ModeSetter, m.Mode != ""
	}
	return "", "", false
}

func (m *MiioModelMap) AllProps() []string {
	seen := map[string]bool{}
	var props []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			props = append(props, p)
		}
	}
	add(m.Power)
	add(m.Bright)
	add(m.CT)
	add(m.Mode)
	for _, p := range m.ExtraRead {
		add(p)
	}
	return props
}

func setterName(prop, setter string) string {
	if setter != "" {
		return setter
	}
	if prop == "" {
		return ""
	}
	if prop == "on" {
		return "set_on"
	}
	if prop == "power" {
		return "set_power"
	}
	return "set_" + prop
}

func miioSetPropertyMapped(dev *Device, model, highLevel string, value interface{}) error {
	m := lookupMiioModel(model)
	if m == nil {
		raw, err := dev.Send("set_prop", []interface{}{highLevel, value})
		if err != nil {
			return err
		}
		var resp struct {
			Result []interface{} `json:"result"`
		}
		if json.Unmarshal(raw, &resp) != nil {
			return fmt.Errorf("unexpected response")
		}
		return nil
	}
	miioName, setter, ok := m.ResolveProp(highLevel)
	if !ok {
		return fmt.Errorf("unknown property %s for model %s", highLevel, model)
	}
	method := setterName(miioName, setter)
	if method == "" {
		switch v := value.(type) {
		case bool:
			if v {
				return miioSetPowerOrOn(dev, miioName, "on")
			}
			return miioSetPowerOrOn(dev, miioName, "off")
		}
	}
	raw, err := dev.Send(method, []interface{}{value})
	if err != nil {
		return err
	}
	var resp struct {
		Result []interface{} `json:"result"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return fmt.Errorf("unexpected response")
	}
	return nil
}

func miioSetPowerOrOn(dev *Device, prop, state string) error {
	switch prop {
	case "power", "pow":
		_, err := miioSetPower(dev, state)
		return err
	default:
		method := "set_" + prop
		raw, err := dev.Send(method, []interface{}{state})
		if err != nil {
			return err
		}
		var resp struct {
			Result []interface{} `json:"result"`
		}
		return json.Unmarshal(raw, &resp)
	}
}

func miioReadAllProps(dev *Device, model string) (map[string]interface{}, error) {
	m := lookupMiioModel(model)
	if m == nil {
		return nil, fmt.Errorf("no property map for model %s", model)
	}
	props := m.AllProps()
	if len(props) == 0 {
		return nil, fmt.Errorf("no known properties for model %s", model)
	}
	vals, err := miioGetProps(dev, props)
	if err != nil {
		return nil, err
	}
	result := make(map[string]interface{}, len(props))
	for i, p := range props {
		if i < len(vals) {
			result[p] = vals[i]
		}
	}
	return result, nil
}
