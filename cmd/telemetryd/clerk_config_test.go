package main

import (
	"encoding/base64"
	"testing"
)

func TestClerkOriginBinding(t *testing.T) {
	key := "pk_test_" + base64.RawStdEncoding.EncodeToString([]byte("clerk.example$"))
	if err := validateClerkConfig(key, "https://clerk.example", "https://server.example:8443"); err != nil {
		t.Fatal(err)
	}
	for _, v := range [][3]string{{key, "https://other.example", "https://server.example"}, {key, "https://clerk.example", "http://server.example"}, {key, "https://clerk.example", "https://server.example/path"}, {"pk_test_bad", "https://clerk.example", "https://server.example"}} {
		if validateClerkConfig(v[0], v[1], v[2]) == nil {
			t.Fatalf("accepted %v", v)
		}
	}
}
