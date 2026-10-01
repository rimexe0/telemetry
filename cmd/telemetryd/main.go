// telemetryd is the headless core service. It has no desktop dependency.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rimexe0/telemetry/internal/clerkidentity"
	"github.com/rimexe0/telemetry/internal/enrollment"
	"github.com/rimexe0/telemetry/internal/httpapi"
	"github.com/rimexe0/telemetry/internal/mcpapi"
	"github.com/rimexe0/telemetry/internal/panelui"
	"github.com/rimexe0/telemetry/internal/store"
)

func main() {
	var (
		listen            = flag.String("listen", env("TELEMETRY_LISTEN", "127.0.0.1:8080"), "HTTP listen address")
		databaseURL       = flag.String("database-url", os.Getenv("TELEMETRY_DATABASE_URL"), "PostgreSQL connection URL")
		ingestToken       = flag.String("ingest-token", os.Getenv("TELEMETRY_INGEST_TOKEN"), "collector bearer token")
		readToken         = flag.String("read-token", os.Getenv("TELEMETRY_READ_TOKEN"), "bounded read bearer token")
		panelToken        = flag.String("panel-token", os.Getenv("TELEMETRY_PANEL_TOKEN"), "owner panel login token")
		panelSecret       = flag.String("panel-session-secret", os.Getenv("TELEMETRY_PANEL_SESSION_SECRET"), "panel session signing secret")
		panelDir          = flag.String("panel-dir", os.Getenv("TELEMETRY_PANEL_DIR"), "SvelteKit static build directory")
		panelCookieSecure = flag.Bool("panel-cookie-secure", envBool("TELEMETRY_PANEL_COOKIE_SECURE", true), "mark panel session cookies secure")
		migrate           = flag.Bool("migrate", false, "apply embedded migrations and exit")
	)
	flag.Parse()
	ingestDeviceTokens := tokenMapEnv("TELEMETRY_INGEST_DEVICE_TOKENS")
	if *databaseURL == "" {
		log.Fatal("TELEMETRY_DATABASE_URL or -database-url is required")
	}
	context, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	database, err := store.OpenPostgres(context, *databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	devices, err := enrollment.Open(context, *databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer devices.Close()
	if *migrate {
		if err := database.ApplyMigrations(context); err != nil {
			log.Fatal(err)
		}
		if err := devices.Migrate(context); err != nil {
			log.Fatal(err)
		}
		fmt.Println("migrations applied")
		return
	}
	clerkKey := os.Getenv("TELEMETRY_CLERK_PUBLISHABLE_KEY")
	clerkIssuer := os.Getenv("TELEMETRY_CLERK_ISSUER")
	ownerID := os.Getenv("TELEMETRY_CLERK_OWNER_ID")
	publicURL := os.Getenv("TELEMETRY_PUBLIC_URL")
	if err := validateClerkConfig(clerkKey, clerkIssuer, publicURL); err != nil {
		log.Fatal(err)
	}
	var ownerAuthorized func(*http.Request) bool
	if clerkKey != "" {
		ownerAuthorized = func(*http.Request) bool { return false }
		if ownerID != "" {
			verifier, err := clerkidentity.New(clerkidentity.Config{Issuer: clerkIssuer, JWKSURL: clerkIssuer + "/.well-known/jwks.json", OwnerUserID: ownerID, AuthorizedParties: []string{publicURL}})
			if err != nil {
				log.Fatal(err)
			}
			ownerAuthorized = func(r *http.Request) bool {
				_, err := verifier.VerifyAuthorization(r.Context(), r.Header.Get("Authorization"))
				return err == nil
			}
		} else {
			log.Print("Clerk sign-in setup only: owner API access disabled until TELEMETRY_CLERK_OWNER_ID is configured")
		}
	}
	// A fresh key per start: the app requests a new device session each time
	// it opens the panel, so a restart only invalidates open in-app views.
	deviceSessionKey := make([]byte, 32)
	if _, err := rand.Read(deviceSessionKey); err != nil {
		log.Fatal(err)
	}
	deviceSessions := httpapi.NewDeviceSessions(deviceSessionKey, 12*time.Hour)
	server, err := httpapi.New(database, httpapi.Config{
		IngestToken:         *ingestToken,
		OwnerAuthorized:     ownerAuthorized,
		DeviceAuthorized:    devices.AuthorizeDevice,
		PairingEnabled:      clerkKey != "" && ownerID != "",
		DeviceSessions:      deviceSessions,
		ClerkPublishableKey: clerkKey,
		IngestDeviceTokens:  ingestDeviceTokens,
		ReadToken:           *readToken,
		PanelToken:          *panelToken,
		PanelSessionSecret:  *panelSecret,
		PanelCookieSecure:   *panelCookieSecure,
	})
	if err != nil {
		log.Fatal(err)
	}
	publicHosts := []string{}
	if parsed, err := url.Parse(publicURL); err == nil && parsed.Hostname() != "" {
		publicHosts = append(publicHosts, parsed.Hostname())
	}
	mcpHandler, err := mcpapi.New(database, *readToken, publicHosts...)
	if err != nil {
		log.Fatal(err)
	}
	router := http.NewServeMux()
	router.Handle("/mcp", mcpHandler)
	if clerkKey != "" && ownerID != "" {
		pairing, err := enrollment.NewHandler(devices, enrollment.HandlerConfig{VerifyOwner: ownerAuthorized, PublicURL: publicURL})
		if err != nil {
			log.Fatal(err)
		}
		router.Handle("/v1/pairing/", pairing)
		router.Handle("/v1/devices", pairing)
		router.Handle("/v1/devices/", pairing)
	}
	if *panelDir != "" {
		panel, err := panelui.NewWithClerk(*panelDir, clerkIssuer)
		if err != nil {
			log.Fatalf("load panel: %v", err)
		}
		router.Handle("GET /panel", http.RedirectHandler("/panel/", http.StatusTemporaryRedirect))
		router.Handle("GET /panel/", http.StripPrefix("/panel/", panel))
	}
	router.Handle("/", server)
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-context.Done()
		shutdownContext, shutdownCancel := contextWithTimeout()
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownContext)
	}()
	go runSilenceChecker(context, database)
	log.Printf("Telemetry core listening on %s", *listen)
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func contextWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		log.Fatalf("%s must be true or false", name)
	}
	return parsed
}

func tokenMapEnv(name string) map[string]string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		log.Fatalf("%s must be a JSON object of device IDs to tokens: %v", name, err)
	}
	for deviceID, token := range values {
		if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(token) == "" {
			log.Fatalf("%s cannot contain empty device IDs or tokens", name)
		}
	}
	return values
}
