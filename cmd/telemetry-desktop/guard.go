package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// guard starts the collector's launchd job again when it is not loaded, or
// is loaded but launchd can no longer start it. It runs every minute twice
// over: as the owner (com.rime.telemetry.guard) and as the system
// (com.rime.telemetry.watchdog, see install-watchdog.sh), so stopping the
// collector takes more than unloading it or switching it off in Login Items.
// The collector's own KeepAlive restarts it after a crash or a quit, but not
// after it was unloaded, which is how it was stopped for nine days in October
// 2026. Telemetry itself stays an ordinary app of the owner's.
//
// Run as the system, -uid names the owner whose collector is watched, and
// -restore is a copy of the collector's job file kept where only the system
// can write, put back when the owner's copy is deleted.
func guard(arguments []string) error {
	flags := flag.NewFlagSet("guard", flag.ExitOnError)
	label := flags.String("label", "com.rime.telemetry.desktop", "the collector's launchd label")
	plist := flags.String("plist", os.ExpandEnv("$HOME/Library/LaunchAgents/com.rime.telemetry.desktop.plist"), "the collector's launchd job file")
	uid := flags.Int("uid", os.Getuid(), "the user whose collector is watched")
	restore := flags.String("restore", "", "a copy of the job file to put back when -plist is missing")
	flags.Parse(arguments)
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("guard runs on macOS only")
	}
	domain := fmt.Sprintf("gui/%d", *uid)
	// Nobody logged in to the screen: there is nothing to start.
	if exec.Command("launchctl", "print", domain).Run() != nil {
		return nil
	}
	output, err := exec.Command("launchctl", "print", domain+"/"+*label).CombinedOutput()
	action := guardAction(err == nil, string(output))
	if action == "" {
		return nil
	}
	if *restore != "" {
		if err := restoreJob(*restore, *plist, *uid); err != nil {
			log.Printf("guard: %v", err)
		}
	}
	if action == "reload" {
		log.Printf("guard: launchd cannot start %s; loading it again", *label)
		exec.Command("launchctl", "bootout", domain+"/"+*label).Run()
		for exec.Command("launchctl", "print", domain+"/"+*label).Run() == nil {
			// bootout returns before the job is gone.
			exec.Command("sleep", "0.5").Run()
		}
	} else {
		log.Printf("guard: %s is not loaded; starting it", *label)
	}
	// A job disabled with launchctl or in Login Items stays unloadable until
	// enabled.
	if output, err := exec.Command("launchctl", "enable", domain+"/"+*label).CombinedOutput(); err != nil {
		log.Printf("guard: enable: %v %s", err, output)
	}
	if output, err := exec.Command("launchctl", "bootstrap", domain, *plist).CombinedOutput(); err != nil {
		return fmt.Errorf("guard: could not start %s: %v %s", *label, err, output)
	}
	log.Printf("guard: started %s", *label)
	return nil
}

// guardAction decides from `launchctl print` what the job needs: "" when it
// is running or launchd is restarting it as usual, "bootstrap" when it is
// not loaded, and "reload" when it is loaded but launchd refuses to start it
// (exit 78 after its program was replaced).
func guardAction(loaded bool, printed string) string {
	if !loaded {
		return "bootstrap"
	}
	if strings.Contains(printed, "state = running") {
		return ""
	}
	if strings.Contains(printed, "spawn failed") || strings.Contains(printed, "last exit code = 78") {
		return "reload"
	}
	return ""
}

// restoreJob puts the system's copy of the job file back when the owner's
// is missing, owned by the owner so launchd loads it for them.
func restoreJob(source, target string, uid int) error {
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read the kept job file: %w", err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("put back %s: %w", target, err)
	}
	log.Printf("guard: put back %s", target)
	if os.Getuid() == 0 && uid != 0 {
		gid := uid
		if output, err := exec.Command("id", "-g", strconv.Itoa(uid)).Output(); err == nil {
			if parsed, err := strconv.Atoi(strings.TrimSpace(string(output))); err == nil {
				gid = parsed
			}
		}
		return os.Chown(target, uid, gid)
	}
	return nil
}
