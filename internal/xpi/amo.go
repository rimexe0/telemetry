package xpi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AMO signs a package as an unlisted add-on with Mozilla's signing API
// (addons.mozilla.org API v5): signed, but listed nowhere, for installing
// from the collector. Firefox installs only signed add-ons; LibreWolf can
// do without.
type AMO struct {
	// Base is the API's origin; empty means addons.mozilla.org.
	Base string
	// Issuer and Secret are the API credentials (JWT issuer and secret).
	Issuer, Secret string
	HTTP           *http.Client
	// Poll is the wait between status checks; Timeout bounds the whole
	// signing.
	Poll, Timeout time.Duration
	Now           func() time.Time
}

// Sign returns Mozilla's signed copy of the package. Mozilla signs each
// version once: a version already submitted is downloaded instead of
// uploaded again, and fresh reports whether this call submitted it.
func (a AMO) Sign(ctx context.Context, xpi []byte) (signed []byte, fresh bool, err error) {
	info, err := Read(xpi)
	if err != nil {
		return nil, false, err
	}
	if info.ID == "" || info.Version == "" {
		return nil, false, errors.New("the package names no ID or version")
	}
	if a.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.Timeout)
		defer cancel()
	}
	version, found, err := a.version(ctx, info.ID, info.Version)
	if err != nil {
		return nil, false, err
	}
	if !found {
		upload, err := a.upload(ctx, xpi)
		if err != nil {
			return nil, false, err
		}
		// Creates the add-on with this ID, or a new version of it.
		body, _ := json.Marshal(map[string]any{"version": map[string]any{"upload": upload}})
		if _, err := a.call(ctx, http.MethodPut, "/api/v5/addons/addon/"+url.PathEscape(info.ID)+"/", "application/json", body); err != nil {
			return nil, false, fmt.Errorf("submit %s: %w", info.Version, err)
		}
		fresh = true
	}
	for {
		if found && version.File.Status == "public" && version.File.URL != "" {
			signed, err := a.call(ctx, http.MethodGet, version.File.URL, "", nil)
			if err != nil {
				return nil, fresh, fmt.Errorf("download the signed package: %w", err)
			}
			return signed, fresh, nil
		}
		if found && version.File.Status == "disabled" {
			return nil, fresh, fmt.Errorf("Mozilla did not sign %s %s", info.ID, info.Version)
		}
		if err := a.wait(ctx); err != nil {
			return nil, fresh, fmt.Errorf("waiting for Mozilla to sign %s: %w", info.Version, err)
		}
		if version, found, err = a.version(ctx, info.ID, info.Version); err != nil {
			return nil, fresh, err
		}
	}
}

type amoVersion struct {
	Version string `json:"version"`
	File    struct {
		URL    string `json:"url"`
		Status string `json:"status"`
	} `json:"file"`
}

// version looks up one version of the add-on by its number; found is false
// when the add-on or the version does not exist yet.
func (a AMO) version(ctx context.Context, id, number string) (amoVersion, bool, error) {
	// A number without a dot would be read as the version's database ID.
	if !strings.Contains(number, ".") {
		number = "v" + number
	}
	body, err := a.call(ctx, http.MethodGet, "/api/v5/addons/addon/"+url.PathEscape(id)+"/versions/"+url.PathEscape(number)+"/", "", nil)
	var status statusError
	if errors.As(err, &status) && status.code == http.StatusNotFound {
		return amoVersion{}, false, nil
	}
	if err != nil {
		return amoVersion{}, false, fmt.Errorf("look up version %s: %w", number, err)
	}
	var version amoVersion
	if err := json.Unmarshal(body, &version); err != nil {
		return amoVersion{}, false, fmt.Errorf("version %s: %w", number, err)
	}
	return version, true, nil
}

// upload sends the package for validation and waits until it is processed.
func (a AMO) upload(ctx context.Context, xpi []byte) (string, error) {
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	writer.WriteField("channel", "unlisted")
	part, err := writer.CreateFormFile("upload", "telemetry-extension.xpi")
	if err != nil {
		return "", err
	}
	part.Write(xpi)
	writer.Close()
	body, err := a.call(ctx, http.MethodPost, "/api/v5/addons/upload/", writer.FormDataContentType(), form.Bytes())
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	for {
		var upload struct {
			UUID       string          `json:"uuid"`
			Processed  bool            `json:"processed"`
			Valid      bool            `json:"valid"`
			Validation json.RawMessage `json:"validation"`
		}
		if err := json.Unmarshal(body, &upload); err != nil || upload.UUID == "" {
			return "", fmt.Errorf("upload answer: %s", body)
		}
		if upload.Processed {
			if !upload.Valid {
				return "", fmt.Errorf("Mozilla's validation refused the package: %s", upload.Validation)
			}
			return upload.UUID, nil
		}
		if err := a.wait(ctx); err != nil {
			return "", fmt.Errorf("waiting for validation: %w", err)
		}
		if body, err = a.call(ctx, http.MethodGet, "/api/v5/addons/upload/"+url.PathEscape(upload.UUID)+"/", "", nil); err != nil {
			return "", fmt.Errorf("upload status: %w", err)
		}
	}
}

func (a AMO) wait(ctx context.Context) error {
	poll := a.Poll
	if poll == 0 {
		poll = 5 * time.Second
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(poll):
		return nil
	}
}

type statusError struct {
	code int
	body string
}

func (e statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

// call makes one authenticated request. path is relative to Base, or a full
// address on it, such as a signed file's.
func (a AMO) call(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	base := strings.TrimSuffix(a.Base, "/")
	if base == "" {
		base = "https://addons.mozilla.org"
	}
	address := path
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		address = base + path
	}
	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// The credentials go only to the API's own origin.
	if origin, err := url.Parse(base); err == nil && request.URL.Host == origin.Host {
		token, err := a.token()
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "JWT "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, statusError{code: response.StatusCode, body: strings.TrimSpace(string(data))}
	}
	return data, nil
}

// token is a JWT for one request: HMAC-SHA256 over the issuer, a nonce and
// a one-minute life, as the API asks (at most five).
func (a AMO) token() (string, error) {
	if a.Issuer == "" || a.Secret == "" {
		return "", errors.New("no signing credentials")
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	issued := now().Unix()
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": a.Issuer, "jti": hex.EncodeToString(nonce), "iat": issued, "exp": issued + 60})
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode(header) + "." + encode(claims)
	mac := hmac.New(sha256.New, []byte(a.Secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + encode(mac.Sum(nil)), nil
}
