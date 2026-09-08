// Package deviceauth retains per-device enrollment credentials outside source control.
package deviceauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	ServerURL  string `json:"server_url"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Secret     string `json:"secret"`
	PairingID  string `json:"pairing_id,omitempty"`
	Approved   bool   `json:"approved"`
}

func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Telemetry", "device.json")
}
func Load(path string) (Config, error) {
	var c Config
	info, err := os.Lstat(path)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return c, errors.New("device config must be a small owner-only regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, errors.New("invalid device config")
	}
	if c.DeviceID == "" || c.Secret == "" || c.ServerURL == "" {
		return c, errors.New("incomplete device config")
	}
	return c, nil
}
func Save(path string, c Config) error {
	if path == "" {
		return errors.New("device config path is required")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".device-pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
