package main

import (
	"context"
	"fmt"
	"io"

	"github.com/rimexe0/telemetry/internal/signin"
)

// runResetSignIn turns the panel token back on in the stored sign-in settings,
// for an owner who can no longer sign in. A running server picks the change
// up within a minute.
func runResetSignIn(ctx context.Context, store signin.Store, out io.Writer, getenv func(string) string) error {
	settings, err := signin.Reset(ctx, store)
	if signin.NotMigrated(err) {
		fmt.Fprintln(out, "Sign-in settings were never changed, so the panel token is already on. Run telemetryd -migrate before changing sign-in in the panel.")
		return nil
	}
	if err != nil {
		return err
	}
	clerk := "off"
	if settings.ClerkEnabled {
		clerk = "still on for " + settings.Clerk.OwnerUserID
	}
	fmt.Fprintf(out, "Panel token sign-in is on again. Clerk sign-in is %s.\n", clerk)
	fmt.Fprintln(out, "A running server picks this up within a minute; restart it if the sign-in screen does not change.")
	if getenv("TELEMETRY_CLERK_PUBLISHABLE_KEY") != "" {
		fmt.Fprintln(out, "Note: TELEMETRY_CLERK_PUBLISHABLE_KEY is set, so this server uses the Clerk configuration from its environment and ignores these settings.")
	} else if getenv("TELEMETRY_PANEL_TOKEN") == "" || getenv("TELEMETRY_PANEL_SESSION_SECRET") == "" {
		fmt.Fprintln(out, "Note: TELEMETRY_PANEL_TOKEN or TELEMETRY_PANEL_SESSION_SECRET is not set here. The server needs both for the panel token to work; they are in the server's environment file.")
	}
	return nil
}
