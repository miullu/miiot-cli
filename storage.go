package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DeviceEntry struct {
	Name  string
	Host  string
	Token string
}

func dataDir() string {
	if dir := os.Getenv("MIIOT_CLI_PATH"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "miiot-cli")
	}
	return filepath.Join(home, ".cache", "miiot-cli")
}

func devicesPath() string {
	return filepath.Join(dataDir(), "devices.csv")
}

func ensureDataDir() error {
	return os.MkdirAll(dataDir(), 0755)
}

func LoadDevices() ([]DeviceEntry, error) {
	path := devicesPath()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	var devices []DeviceEntry
	for i, rec := range records {
		if i == 0 && len(rec) >= 3 && rec[0] == "name" {
			continue
		}
		if len(rec) >= 3 {
			devices = append(devices, DeviceEntry{
				Name:  strings.TrimSpace(rec[0]),
				Host:  strings.TrimSpace(rec[1]),
				Token: strings.TrimSpace(rec[2]),
			})
		}
	}
	return devices, nil
}

func SaveDevices(devices []DeviceEntry) error {
	if err := ensureDataDir(); err != nil {
		return err
	}
	path := devicesPath()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	w.Write([]string{"name", "ip", "token"})
	for _, d := range devices {
		w.Write([]string{d.Name, d.Host, d.Token})
	}
	w.Flush()
	return w.Error()
}

func FindDevice(name string) (*DeviceEntry, error) {
	devices, err := LoadDevices()
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if d.Name == name {
			return &d, nil
		}
	}
	return nil, fmt.Errorf("device '%s' not found", name)
}

func AddDevice(name, host, token string) error {
	devices, err := LoadDevices()
	if err != nil {
		return err
	}
	for _, d := range devices {
		if d.Name == name {
			return fmt.Errorf("device '%s' already exists", name)
		}
	}
	devices = append(devices, DeviceEntry{Name: name, Host: host, Token: token})
	return SaveDevices(devices)
}

func RemoveDevice(name string) error {
	devices, err := LoadDevices()
	if err != nil {
		return err
	}
	found := false
	var updated []DeviceEntry
	for _, d := range devices {
		if d.Name == name {
			found = true
		} else {
			updated = append(updated, d)
		}
	}
	if !found {
		return fmt.Errorf("device '%s' not found", name)
	}
	return SaveDevices(updated)
}
