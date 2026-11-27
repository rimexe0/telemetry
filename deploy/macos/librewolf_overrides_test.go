package macos_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const signaturesLine = `defaultPref("xpinstall.signatures.required", false);`

// overridesFunction is librewolf_overrides as a script defines it.
func overridesFunction(t *testing.T, script string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", script))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "librewolf_overrides() {")
	if start < 0 {
		t.Fatalf("%s has no librewolf_overrides", script)
	}
	end := strings.Index(text[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("%s: librewolf_overrides does not end", script)
	}
	return text[start : start+end+3]
}

// The Mac and Linux installers and updaters put the line LibreWolf needs
// for an unsigned package into the user's overrides file once, keep what
// else the owner wrote there, and take that line out again once the
// package is signed.
func TestLibreWolfOverridesFollowTheKeptPackage(t *testing.T) {
	for _, script := range []struct{ path, shell, folder string }{
		{"deploy/macos/install-mac.sh", "zsh", "root"},
		{"deploy/macos/telemetry-update.sh", "zsh", "root"},
		{"deploy/linux/install-linux.sh", "bash", "dir"},
		{"deploy/linux/telemetry-update.sh", "bash", "DIR"},
	} {
		t.Run(script.path, func(t *testing.T) {
			if _, err := exec.LookPath(script.shell); err != nil {
				t.Skip(script.shell + " is not installed")
			}
			function := overridesFunction(t, script.path)
			home := t.TempDir()
			kept := t.TempDir()
			xpi := filepath.Join(kept, "extension.xpi")
			file := filepath.Join(home, ".librewolf", "librewolf.overrides.cfg")
			run := func() {
				t.Helper()
				command := exec.Command(script.shell, "-c", function+"\n"+script.folder+"="+kept+"\nlibrewolf_overrides")
				command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("librewolf_overrides: %v %s", err, output)
				}
			}
			read := func() string {
				t.Helper()
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}

			// No package kept yet: nothing is written.
			run()
			if _, err := os.Stat(file); err == nil {
				t.Fatal("wrote overrides without a package")
			}

			// Unsigned, with an overrides file of the owner's that has no
			// final newline: the line is added once, on its own line.
			os.WriteFile(xpi, []byte("PK manifest.json"), 0o644)
			os.MkdirAll(filepath.Dir(file), 0o755)
			os.WriteFile(file, []byte(`defaultPref("privacy.resistFingerprinting", false);`), 0o644)
			run()
			run()
			want := `defaultPref("privacy.resistFingerprinting", false);` + "\n" + signaturesLine + "\n"
			if got := read(); got != want {
				t.Fatalf("unsigned = %q", got)
			}

			// The line written by hand is not written twice.
			os.WriteFile(file, []byte(signaturesLine+"\n"), 0o644)
			run()
			if got := read(); got != signaturesLine+"\n" {
				t.Fatalf("by hand = %q", got)
			}

			// Signed: that exact line goes, the rest stays.
			os.WriteFile(file, []byte("// mine\n"+signaturesLine+"\n"+`defaultPref("a", 1);`+"\n"), 0o644)
			os.WriteFile(xpi, []byte("PK META-INF/mozilla.rsa manifest.json"), 0o644)
			run()
			if got := read(); got != "// mine\n"+`defaultPref("a", 1);`+"\n" {
				t.Fatalf("signed = %q", got)
			}
			run()
			if got := read(); got != "// mine\n"+`defaultPref("a", 1);`+"\n" {
				t.Fatalf("signed again = %q", got)
			}
		})
	}
}

// Without LibreWolf on Linux there is no overrides file to write.
func TestLibreWolfOverridesNeedLibreWolfOnLinux(t *testing.T) {
	function := overridesFunction(t, "deploy/linux/install-linux.sh")
	home, kept := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(kept, "extension.xpi"), []byte("PK manifest.json"), 0o644)
	command := exec.Command("bash", "-c", function+"\ndir="+kept+"\nlibrewolf_overrides")
	command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("librewolf_overrides: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(home, ".librewolf")); err == nil {
		t.Fatal("made a LibreWolf folder without LibreWolf")
	}
}
