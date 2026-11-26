//go:build darwin

package desktop

import "testing"

// A request from LibreWolf, whichever of its processes makes it, is
// LibreWolf's.
func TestConnectionBelongsToTheOutermostApp(t *testing.T) {
	for path, want := range map[string]string{
		"/Applications/LibreWolf.app/Contents/MacOS/librewolf":                                                                           "/Applications/LibreWolf.app",
		"/Applications/LibreWolf.app/Contents/MacOS/plugin-container.app/Contents/MacOS/plugin-container":                                "/Applications/LibreWolf.app",
		"/Applications/Helium.app/Contents/Frameworks/Helium Framework.framework/Helpers/Helium Helper.app/Contents/MacOS/Helium Helper": "/Applications/Helium.app",
		"/usr/bin/curl": "",
	} {
		if got := outerApp(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}
