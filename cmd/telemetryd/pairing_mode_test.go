package main

import "testing"

func TestPairingMode(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		clerkKey, ownerID, token, secret, publicURL string
		clerk, panel                                bool
	}{
		{"clerk owner", "pk_test_x", "user_1", "", "", "https://t.example", true, false},
		{"clerk without owner", "pk_test_x", "", "", "", "https://t.example", false, false},
		{"clerk wins over a panel token", "pk_test_x", "user_1", "token", "secret", "https://t.example", true, false},
		{"panel token", "", "", "token", "secret", "https://t.example", false, true},
		{"panel token without public URL", "", "", "token", "secret", "", false, false},
		{"panel token without session secret", "", "", "token", "", "https://t.example", false, false},
		{"nothing", "", "", "", "", "", false, false},
	} {
		clerk, panel := pairingMode(tc.clerkKey, tc.ownerID, tc.token, tc.secret, tc.publicURL)
		if clerk != tc.clerk || panel != tc.panel {
			t.Errorf("%s: got clerk=%v panel=%v", tc.name, clerk, panel)
		}
	}
}
