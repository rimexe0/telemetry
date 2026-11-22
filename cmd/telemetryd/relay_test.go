package main

import "testing"

func TestOnlyAPublicHostNameIsSavedAsTheRelay(t *testing.T) {
	for host, want := range map[string]bool{
		"t.rime.cyou": true, "relay.example.com": true,
		"": false, "localhost": false, "127.0.0.1": false, "127.0.0.1:8787": false, "t.rime.cyou:443": false,
		"[::1]": false, "datalix.tail3419e.ts.net": false, "plagiarism.local": false, "relay": false,
	} {
		if got := publicHost(host); got != want {
			t.Errorf("%q = %v, want %v", host, got, want)
		}
	}
}
