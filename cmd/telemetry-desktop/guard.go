package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
)

// guard starts the collector's launchd job again when it is not loaded. A
// separate launchd job runs it every minute: the collector's own KeepAlive
// restarts it after a crash or a quit, but not after it was unloaded, which
// is how it was stopped for five days in October 2026. When it cannot be
// started (switched off in Login Items), the server notices that the Mac is
// on without it and keeps telling the owner.
func guard(arguments []string) error {
	flags := flag.NewFlagSet("guard", flag.ExitOnError)
	label := flags.String("label", "com.rime.telemetry.desktop", "the collector's launchd label")
	plist := flags.String("plist", os.ExpandEnv("$HOME/Library/LaunchAgents/com.rime.telemetry.desktop.plist"), "the collector's launchd job file")
	flags.Parse(arguments)
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("guard runs on macOS only")
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if exec.Command("launchctl", "print", domain+"/"+*label).Run() == nil {
		return nil
	}
	log.Printf("guard: %s is not loaded; starting it", *label)
	// A job disabled with launchctl stays unloadable until enabled.
	if output, err := exec.Command("launchctl", "enable", domain+"/"+*label).CombinedOutput(); err != nil {
		log.Printf("guard: enable: %v %s", err, output)
	}
	if output, err := exec.Command("launchctl", "bootstrap", domain, *plist).CombinedOutput(); err != nil {
		return fmt.Errorf("guard: could not start %s: %v %s", *label, err, output)
	}
	log.Printf("guard: started %s", *label)
	return nil
}
