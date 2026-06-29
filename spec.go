package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	cacheDir   = ".cache/miiot-cli"
	specSubdir = "specs"
	protoFile  = "protocols.json"

	instancesURL = "https://miot-spec.org/miot-spec-v2/instances?status=all"
	instanceURL  = "https://miot-spec.org/miot-spec-v2/instance?type=%s"
)

type MiotInstances struct {
	Instances []MiotInstance `json:"instances"`
}

type MiotInstance struct {
	Model  string `json:"model"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
}

type MiotSpec struct {
	Type     string       `json:"type"`
	Services []MiotService `json:"services"`
}

type MiotService struct {
	IID        int           `json:"iid"`
	Type       string        `json:"type"`
	Properties []MiotProperty `json:"properties"`
	Actions    []interface{} `json:"actions,omitempty"`
}

type MiotProperty struct {
	IID   int    `json:"iid"`
	Type  string `json:"type"`
	Format string `json:"format,omitempty"`
	Access []string `json:"access,omitempty"`
}

func cacheBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, cacheDir), nil
}

func specCacheDir() (string, error) {
	base, err := cacheBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, specSubdir), nil
}

func specCachePath(model string) (string, error) {
	dir, err := specCacheDir()
	if err != nil {
		return "", err
	}
	name := strings.ReplaceAll(model, "/", "_") + ".json"
	return filepath.Join(dir, name), nil
}

func loadCachedSpec(model string) (*MiotSpec, error) {
	path, err := specCachePath(model)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec MiotSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func saveSpecToCache(model string, spec *MiotSpec) error {
	dir, err := specCacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path, err := specCachePath(model)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func fetchJSON(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "miiot-cli/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func findModelURN(model string) (string, error) {
	data, err := fetchJSON(instancesURL)
	if err != nil {
		return "", err
	}
	var instances MiotInstances
	if err := json.Unmarshal(data, &instances); err != nil {
		return "", err
	}
	for _, inst := range instances.Instances {
		if inst.Model == model {
			return inst.Type, nil
		}
	}
	return "", fmt.Errorf("model %s not found in MIoT spec registry", model)
}

func fetchSpec(urn string) (*MiotSpec, error) {
	url := fmt.Sprintf(instanceURL, urn)
	data, err := fetchJSON(url)
	if err != nil {
		return nil, err
	}
	var spec MiotSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func getSpec(model string, force bool) (*MiotSpec, error) {
	if !force {
		cached, err := loadCachedSpec(model)
		if err == nil && cached != nil {
			return cached, nil
		}
	}
	fmt.Fprintf(os.Stderr, "Fetching spec for %s...\n", model)
	urn, err := findModelURN(model)
	if err != nil {
		return nil, err
	}
	spec, err := fetchSpec(urn)
	if err != nil {
		return nil, err
	}
	if err := saveSpecToCache(model, spec); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to cache spec: %v\n", err)
	}
	return spec, nil
}

func loadProtocols() map[string]string {
	base, err := cacheBaseDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(base, protoFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var protos map[string]string
	if err := json.Unmarshal(data, &protos); err != nil {
		return nil
	}
	return protos
}

func saveProtocol(model, proto string) {
	base, err := cacheBaseDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return
	}
	path := filepath.Join(base, protoFile)
	protos := loadProtocols()
	if protos == nil {
		protos = make(map[string]string)
	}
	protos[model] = proto
	data, err := json.MarshalIndent(protos, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0644)
}

func findLightOnProp(spec *MiotSpec) (siid, piid int, ok bool) {
	for _, svc := range spec.Services {
		if !strings.Contains(svc.Type, ":service:light") &&
			!strings.Contains(svc.Type, ":service:switch") {
			continue
		}
		siid = svc.IID
		for _, prop := range svc.Properties {
			if strings.Contains(prop.Type, ":property:on") {
				return siid, prop.IID, true
			}
		}
	}
	return 0, 0, false
}

var propAliases = map[string][]string{
	"colortemp":    {"color-temperature", "color_temp", "colour_temperature"},
	"color_temp":   {"color-temperature", "colortemp", "colour_temperature"},
	"brightness":   {"bright"},
}

func expandPropName(name string) []string {
	if aliases, ok := propAliases[name]; ok {
		return append([]string{name}, aliases...)
	}
	return []string{name}
}

func findPropByName(spec *MiotSpec, name string) (siid, piid int, ok bool) {
	for _, candidate := range expandPropName(name) {
		suffix := ":" + candidate
		for _, svc := range spec.Services {
			siid = svc.IID
			for _, prop := range svc.Properties {
				if strings.HasSuffix(prop.Type, suffix) {
					return siid, prop.IID, true
				}
				if strings.Contains(prop.Type, ":property:"+candidate+":") {
					return siid, prop.IID, true
				}
			}
		}
	}
	return 0, 0, false
}
