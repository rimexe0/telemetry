package signin

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// Environment describes a Clerk sign-in fixed by the server's environment.
// While it is set the stored settings are not used and cannot be changed.
type Environment struct {
	PublishableKey string
	Issuer         string
	OwnerLinked    bool
}

// HandlerConfig connects the settings endpoints to the server.
type HandlerConfig struct {
	// Manager is nil when Environment is set.
	Manager     *Manager
	Environment *Environment
	// VerifyOwner reports whether the request is signed in as the owner.
	VerifyOwner func(*http.Request) bool
	// PublicURL is the only browser origin allowed to change settings.
	PublicURL string
	// PanelTokenConfigured reports whether TELEMETRY_PANEL_TOKEN is set, so
	// the page can say when turning it on would not help.
	PanelTokenConfigured bool
}

// Handler serves /v1/settings/sign-in and its sub-paths.
type Handler struct {
	config        HandlerConfig
	trustedOrigin string
	mux           *http.ServeMux
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.VerifyOwner == nil {
		return nil, errors.New("owner verifier is required")
	}
	if (config.Manager == nil) == (config.Environment == nil) {
		return nil, errors.New("exactly one of manager and environment is required")
	}
	h := &Handler{config: config, mux: http.NewServeMux()}
	if origin, err := canonicalOrigin(config.PublicURL); err == nil {
		h.trustedOrigin = origin
	}
	h.mux.HandleFunc("GET /v1/settings/sign-in", h.get)
	h.mux.HandleFunc("PUT /v1/settings/sign-in", h.setMethods)
	h.mux.HandleFunc("PUT /v1/settings/sign-in/clerk", h.setClerkKey)
	h.mux.HandleFunc("POST /v1/settings/sign-in/clerk/link", h.link)
	h.mux.HandleFunc("POST /v1/settings/sign-in/clerk/unlink", h.unlink)
	return h, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	h.mux.ServeHTTP(writer, request)
}

// View is what GET returns. Nothing in it is secret: the publishable key and
// issuer are public by design and the owner user ID is an account handle.
type View struct {
	ManagedBy  string     `json:"managed_by"`
	PublicURL  string     `json:"public_url"`
	CanChange  bool       `json:"can_change"`
	Blocked    string     `json:"blocked,omitempty"`
	PanelToken PanelView  `json:"panel_token"`
	Clerk      ClerkView  `json:"clerk"`
	Changes    []ViewItem `json:"changes"`
}

type PanelView struct {
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
}

type ClerkView struct {
	Enabled        bool   `json:"enabled"`
	PublishableKey string `json:"publishable_key,omitempty"`
	Issuer         string `json:"issuer,omitempty"`
	Linked         bool   `json:"linked"`
	OwnerUserID    string `json:"owner_user_id,omitempty"`
	// Development is true for a pk_test_ key.
	Development bool `json:"development"`
}

type ViewItem struct {
	ChangedAt string `json:"changed_at"`
	Change    string `json:"change"`
}

func (h *Handler) view(request *http.Request) View {
	if env := h.config.Environment; env != nil {
		return View{
			ManagedBy: "environment", PublicURL: h.config.PublicURL, Blocked: "Sign-in is set by this server's configuration.",
			PanelToken: PanelView{Enabled: false, Configured: h.config.PanelTokenConfigured},
			Clerk:      ClerkView{Enabled: env.OwnerLinked, PublishableKey: env.PublishableKey, Issuer: env.Issuer, Linked: env.OwnerLinked, Development: strings.HasPrefix(env.PublishableKey, "pk_test_")},
			Changes:    []ViewItem{},
		}
	}
	m := h.config.Manager
	settings := m.Settings()
	view := View{
		ManagedBy: "settings", PublicURL: m.PublicURL(), CanChange: m.PublicURL() != "" && h.trustedOrigin != "",
		PanelToken: PanelView{Enabled: settings.PanelTokenEnabled, Configured: h.config.PanelTokenConfigured},
		Clerk: ClerkView{
			Enabled: settings.ClerkEnabled, PublishableKey: settings.Clerk.PublishableKey, Issuer: settings.Clerk.Issuer,
			Linked: settings.Linked(), OwnerUserID: settings.Clerk.OwnerUserID, Development: strings.HasPrefix(settings.Clerk.PublishableKey, "pk_test_"),
		},
		Changes: []ViewItem{},
	}
	if !view.CanChange {
		view.Blocked = "Set TELEMETRY_PUBLIC_URL to this panel's HTTPS address to change sign-in here."
	}
	if changes, err := m.store.Changes(request.Context(), 10); err == nil {
		for _, item := range changes {
			view.Changes = append(view.Changes, ViewItem{ChangedAt: item.ChangedAt.UTC().Format("2006-01-02T15:04:05Z"), Change: item.Change})
		}
	}
	return view
}

func (h *Handler) get(writer http.ResponseWriter, request *http.Request) {
	if !h.config.VerifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner sign-in is required")
		return
	}
	writeJSON(writer, http.StatusOK, h.view(request))
}

// write checks what every change needs: settings mode, the panel's own origin,
// the owner, and one JSON object of the expected shape.
func (h *Handler) write(writer http.ResponseWriter, request *http.Request, body any) bool {
	if h.config.Environment != nil {
		writeError(writer, http.StatusConflict, "managed_by_environment", "Sign-in is set by this server's configuration.")
		return false
	}
	if h.trustedOrigin == "" {
		writeError(writer, http.StatusConflict, "public_url_missing", "Set TELEMETRY_PUBLIC_URL to this panel's HTTPS address to change sign-in here.")
		return false
	}
	if !h.trusted(request) {
		writeError(writer, http.StatusForbidden, "untrusted_origin", "sign-in changes must come from the configured Telemetry origin")
		return false
	}
	if !h.config.VerifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner sign-in is required")
		return false
	}
	if mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_content_type", "send a JSON body")
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func (h *Handler) setMethods(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		PanelTokenEnabled *bool `json:"panel_token_enabled"`
		ClerkEnabled      *bool `json:"clerk_enabled"`
	}
	if !h.write(writer, request, &body) {
		return
	}
	if body.PanelTokenEnabled == nil || body.ClerkEnabled == nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "panel_token_enabled and clerk_enabled are required")
		return
	}
	_, err := h.config.Manager.SetMethods(request.Context(), *body.PanelTokenEnabled, *body.ClerkEnabled)
	h.respond(writer, request, err)
}

func (h *Handler) setClerkKey(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		PublishableKey string `json:"publishable_key"`
	}
	if !h.write(writer, request, &body) {
		return
	}
	_, err := h.config.Manager.SetClerkKey(request.Context(), body.PublishableKey)
	h.respond(writer, request, err)
}

func (h *Handler) link(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		SessionToken string `json:"session_token"`
	}
	if !h.write(writer, request, &body) {
		return
	}
	_, err := h.config.Manager.Link(request.Context(), body.SessionToken)
	h.respond(writer, request, err)
}

func (h *Handler) unlink(writer http.ResponseWriter, request *http.Request) {
	var body struct{}
	if !h.write(writer, request, &body) {
		return
	}
	_, err := h.config.Manager.Unlink(request.Context())
	h.respond(writer, request, err)
}

func (h *Handler) respond(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case err == nil:
		writeJSON(writer, http.StatusOK, h.view(request))
	case errors.Is(err, ErrTokenRejected):
		writeError(writer, http.StatusUnprocessableEntity, "clerk_token_rejected", err.Error())
	case errors.Is(err, ErrRejected):
		writeError(writer, http.StatusConflict, "invalid_settings", strings.TrimPrefix(err.Error(), ErrRejected.Error()+": "))
	case errors.Is(err, ErrUnavailable):
		writeError(writer, http.StatusConflict, "unavailable", strings.TrimPrefix(err.Error(), ErrUnavailable.Error()+": "))
	case NotMigrated(err):
		writeError(writer, http.StatusServiceUnavailable, "not_migrated", "run telemetryd -migrate to create the sign-in settings tables")
	default:
		log.Printf("sign-in: %v", err)
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", "sign-in settings could not be saved")
	}
}

func (h *Handler) trusted(request *http.Request) bool {
	origin, err := canonicalOrigin(request.Header.Get("Origin"))
	return err == nil && origin == h.trustedOrigin
}

func canonicalOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return "", errors.New("must be an absolute origin without a path")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", errors.New("origin scheme must be http or https")
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}

func writeError(writer http.ResponseWriter, status int, code, detail string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "detail": detail}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
