//go:build linux

package desktop

import (
	"net"
	"testing"
)

func TestProcAddressesMatch(t *testing.T) {
	loopback := net.ParseIP("127.0.0.1")
	// 127.0.0.1:54321 as /proc/net/tcp and /proc/net/tcp6 write it.
	if !matchesAddress("0100007F:D431", loopback, 54321) {
		t.Fatal("IPv4 entry not matched")
	}
	if !matchesAddress("0000000000000000FFFF00000100007F:D431", loopback, 54321) {
		t.Fatal("IPv4 client of an IPv6 socket not matched")
	}
	if matchesAddress("0100007F:D432", loopback, 54321) || matchesAddress("0200007F:D431", loopback, 54321) {
		t.Fatal("another port or address matched")
	}
	if !matchesAddress("00000000000000000000000001000000:1F90", net.ParseIP("::1"), 8080) {
		t.Fatal("IPv6 loopback not matched")
	}
	if appName("org.kde.konsole") != "Konsole" || appName("helium") != "Helium" {
		t.Fatal("app names")
	}
}
