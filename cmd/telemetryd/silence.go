package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/rimexe0/telemetry/internal/silence"
	"github.com/rimexe0/telemetry/internal/store"
)

// runSilenceChecker opens and closes collection incidents every minute until
// ctx ends (#28).
func runSilenceChecker(ctx context.Context, repository store.Repository) {
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		evidence, err := repository.CollectionEvidence(checkCtx)
		if err != nil {
			log.Printf("silence check: %v", err)
			return
		}
		var disk syscall.Statfs_t
		if syscall.Statfs("/", &disk) == nil {
			free := uint64(disk.Bavail) * uint64(disk.Bsize)
			evidence.ServerFreeBytes = &free
		}
		now := time.Now().UTC()
		evidence.Desktops = withoutRetired(evidence.Desktops, os.Getenv("TELEMETRY_RETIRED_DEVICES"))
		askMachines(checkCtx, evidence.Desktops, now)
		opened, err := repository.SyncCollectionIncidents(checkCtx, silence.Evaluate(evidence, now), now)
		if err != nil {
			log.Printf("silence check: %v", err)
			return
		}
		for _, incident := range opened {
			log.Printf("collection incident %d: %s %s since %s", incident.ID, incident.Device, incident.Kind, incident.StartedAt.Format(time.RFC3339))
		}
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		check()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// withoutRetired leaves out the machines named in TELEMETRY_RETIRED_DEVICES
// (device IDs separated by spaces or commas): ones that no longer exist, so
// their silence is not a collection problem. A machine is only retired by
// naming it; going quiet for long never retires one, since a device the owner
// switched off for a week must still show.
func withoutRetired(desktops []silence.DesktopStatus, retired string) []silence.DesktopStatus {
	names := map[string]bool{}
	for _, name := range strings.Fields(strings.ReplaceAll(retired, ",", " ")) {
		names[name] = true
	}
	kept := desktops[:0:0]
	for _, desktop := range desktops {
		if !names[desktop.DeviceID] {
			kept = append(kept, desktop)
		}
	}
	return kept
}

// tailscaleNames maps desktop device IDs to their Tailscale names, from
// TELEMETRY_TAILSCALE_DEVICES such as "mac-00000001=plagiarism-1".
func tailscaleNames() map[string]string {
	names := map[string]string{}
	for _, pair := range strings.Fields(strings.ReplaceAll(os.Getenv("TELEMETRY_TAILSCALE_DEVICES"), ",", " ")) {
		if device, name, ok := strings.Cut(pair, "="); ok && device != "" && name != "" {
			names[device] = name
		}
	}
	return names
}

// askMachines pings each machine whose collector has gone quiet, unless it
// said it was going to sleep, so a collector stopped on a computer that is
// still on can be told from one that is asleep or switched off.
func askMachines(ctx context.Context, desktops []silence.DesktopStatus, now time.Time) {
	names := tailscaleNames()
	for i := range desktops {
		status := &desktops[i]
		name, known := names[status.DeviceID]
		if !known || status.State == "sleeping" || now.Sub(status.At) < silence.StoppedWhileOnAfter {
			continue
		}
		on := machineAnswers(ctx, name)
		status.On = &on
	}
}

// machineAnswers pings over Tailscale. A sleeping or switched-off machine
// does not answer; a relayed answer counts.
func machineAnswers(ctx context.Context, name string) bool {
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, _ := exec.CommandContext(pingCtx, "tailscale", "ping", "-c", "2", "--until-direct=false", "--timeout", "4s", name).CombinedOutput()
	return strings.Contains(string(output), "pong from")
}
