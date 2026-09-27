package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const deviceSessionCookieName = "telemetry_device_session"

// DeviceSessions lets a paired owner device open the panel in its own web
// view without a separate login. Sessions are signed, short-lived and are not
// accepted for device enrolment or revocation, which stay Clerk-only.
type DeviceSessions struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewDeviceSessions(secret []byte, ttl time.Duration) *DeviceSessions {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &DeviceSessions{secret: append([]byte(nil), secret...), ttl: ttl, now: time.Now}
}

func (d *DeviceSessions) sign(payload string) string {
	mac := hmac.New(sha256.New, d.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (d *DeviceSessions) issue(deviceID string) (string, time.Time) {
	expires := d.now().UTC().Add(d.ttl)
	payload := base64.RawURLEncoding.EncodeToString([]byte(deviceID)) + "." + strconv.FormatInt(expires.Unix(), 10)
	return payload + "." + d.sign(payload), expires
}

// Authorized reports whether the request carries a valid, unexpired session.
func (d *DeviceSessions) Authorized(request *http.Request) bool {
	if d == nil || len(d.secret) == 0 {
		return false
	}
	cookie, err := request.Cookie(deviceSessionCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(d.sign(payload))) {
		return false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && d.now().Unix() < expires
}

func (s *Server) devicePanelSession(writer http.ResponseWriter, request *http.Request) {
	if s.config.DeviceSessions == nil {
		writeError(writer, http.StatusNotFound, "unavailable", "device sessions are not configured")
		return
	}
	if !s.pairedDeviceAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	value, expires := s.config.DeviceSessions.issue(strings.TrimSpace(request.Header.Get("X-Telemetry-Device-ID")))
	http.SetCookie(writer, &http.Cookie{
		Name: deviceSessionCookieName, Value: value, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.config.PanelCookieSecure, SameSite: http.SameSiteLaxMode,
	})
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"cookie":     fmt.Sprintf("%s=%s", deviceSessionCookieName, value),
		"expires_at": expires,
	})
}
