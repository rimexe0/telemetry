package deviceauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Client struct {
	HTTPClient   *http.Client
	PollInterval time.Duration
}
type HTTPError struct{ Status int }

func (e HTTPError) Error() string { return fmt.Sprintf("pairing returned HTTP %d", e.Status) }
func origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("server must be an HTTPS origin")
	}
	if u.Scheme != "https" {
		return "", errors.New("pairing requires HTTPS")
	}
	return strings.TrimRight(raw, "/"), nil
}
func (c Client) request(ctx context.Context, method, address, secret string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	r, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	if c.HTTPClient != nil {
		copy := *c.HTTPClient
		client = &copy
		if client.Timeout == 0 {
			client.Timeout = 15 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		return errors.New("private Telemetry server is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return HTTPError{response.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return errors.New("invalid pairing response size")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("invalid pairing response")
	}
	return nil
}

// Login retains the secret before starting, so an interrupted HTTP response can be retried.
func (c Client) Login(ctx context.Context, path, server, deviceID, name string, showURL func(string)) (Config, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Config{}, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Config{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return Config{}, errors.New("another login is already using this device config")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	server, err = origin(server)
	if err != nil {
		return Config{}, err
	}
	conf, err := Load(path)
	if errors.Is(err, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return conf, err
		}
		if deviceID == "" {
			id := make([]byte, 12)
			if _, err = rand.Read(id); err != nil {
				return conf, err
			}
			deviceID = "desktop-" + hex.EncodeToString(id)
		}
		if name == "" {
			name, _ = os.Hostname()
		}
		if name == "" {
			name = "Computer"
		}
		conf = Config{ServerURL: server, DeviceID: deviceID, DeviceName: name, Secret: base64.RawURLEncoding.EncodeToString(secret)}
		if err = Save(path, conf); err != nil {
			return conf, err
		}
	} else if err != nil {
		return conf, err
	}
	if conf.ServerURL != server || (deviceID != "" && conf.DeviceID != deviceID) {
		return conf, errors.New("saved pairing belongs to another server or device; use its existing configuration")
	}
	interval := c.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	for {
		if conf.PairingID == "" {
			hash := sha256.Sum256([]byte(conf.Secret))
			var start struct {
				PairingID string `json:"pairing_id"`
				PanelURL  string `json:"panel_url"`
			}
			err = c.request(ctx, "POST", server+"/v1/pairing/sessions", "", map[string]string{"device_id": conf.DeviceID, "device_name": conf.DeviceName, "credential_hash": hex.EncodeToString(hash[:])}, &start)
			if err != nil {
				return conf, err
			}
			target, parseErr := url.Parse(start.PanelURL)
			base, _ := url.Parse(server)
			if parseErr != nil || target.Scheme != base.Scheme || target.Host != base.Host || target.User != nil || target.Path != "/panel/" || start.PairingID == "" || target.Query().Get("pair") != start.PairingID {
				return conf, errors.New("pairing response changed server or session")
			}
			conf.PairingID = start.PairingID
			if err = Save(path, conf); err != nil {
				return conf, err
			}
		}
		if showURL != nil {
			showURL(server + "/panel/?pair=" + url.QueryEscape(conf.PairingID))
			showURL = nil
		}
		var status struct {
			Status   string `json:"status"`
			DeviceID string `json:"device_id"`
		}
		err = c.request(ctx, "GET", server+"/v1/pairing/sessions/"+url.PathEscape(conf.PairingID), conf.Secret, nil, &status)
		if err == nil {
			if status.DeviceID != conf.DeviceID {
				return conf, errors.New("pairing response changed device")
			}
			switch status.Status {
			case "approved":
				conf.Approved = true
				err = Save(path, conf)
				return conf, err
			case "pending":
			default:
				return conf, errors.New("pairing is no longer pending")
			}
		} else {
			var h HTTPError
			if errors.As(err, &h) && h.Status < 500 && h.Status != 408 && h.Status != 429 {
				if h.Status == 410 && !conf.Approved {
					conf.PairingID = ""
					if saveErr := Save(path, conf); saveErr != nil {
						return conf, saveErr
					}
					return conf, errors.New("pairing expired; run login again")
				}
				return conf, err
			}
		}
		select {
		case <-ctx.Done():
			return conf, ctx.Err()
		case <-time.After(interval):
		}
	}
}
