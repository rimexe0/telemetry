package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/rimexe0/telemetry/internal/deviceauth"
	"log"
	"os"
	"os/signal"
)

func login(arguments []string) {
	flags := flag.NewFlagSet("login", flag.ExitOnError)
	server := flags.String("server", "https://server.example.ts.net:8443", "private Telemetry HTTPS origin")
	id := flags.String("device", "", "existing Telemetry device ID; omit only for a new collector")
	name := flags.String("name", "", "device name")
	path := flags.String("config", deviceauth.DefaultPath(), "owner-only device configuration")
	flags.Parse(arguments)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	config, err := (deviceauth.Client{}).Login(ctx, *path, *server, *id, *name, func(address string) { fmt.Printf("Sign in to connect this device:\n%s\n", address) })
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Connected %s. Credentials saved in %s\n", config.DeviceID, *path)
}

// applyPairing never combines a saved credential with a different server/device.
func applyPairing(path string, device, server, token, tokenFile *string) (bool, error) {
	if path == "" {
		return false, nil
	}
	if *token != "" || *tokenFile != "" {
		return false, fmt.Errorf("-config cannot be combined with manual credentials")
	}
	c, err := deviceauth.Load(path)
	if err != nil {
		return false, err
	}
	if (*device != "" && *device != c.DeviceID) || (*server != "" && *server != c.ServerURL) {
		return false, fmt.Errorf("paired configuration differs from requested server/device")
	}
	*device = c.DeviceID
	if !c.Approved {
		*server = ""
		return false, nil
	}
	*server = c.ServerURL
	*token = c.Secret
	return true, nil
}
