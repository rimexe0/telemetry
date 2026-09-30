package policy

import (
	"errors"
	"testing"
)

func TestBlocklistChangesMoveAppsBetweenLists(t *testing.T) {
	config := Config{Essentials: []TargetGroup{{ID: "com.whatsapp", Label: "com.whatsapp", AndroidPackages: []string{"com.whatsapp"}}}}
	next := ApplyBlocklist(config, []BlocklistChange{
		{Action: "block", AndroidPackage: "com.twitter.android"},
		{Action: "block", AndroidPackage: "com.whatsapp"},
		{Action: "unlist", AndroidPackage: "com.reddit.frontpage"},
	})
	if len(next.Distractions) != 2 || len(next.Essentials) != 0 {
		t.Fatalf("got %+v", next)
	}
	if len(config.Essentials) != 1 {
		t.Fatal("the input config must not change")
	}
}

func TestBlocklistRefusesWebsites(t *testing.T) {
	for _, value := range []string{"x.com", "reddit.com", "https://youtube.com", "not a package"} {
		change := BlocklistChange{Action: "block", AndroidPackage: value}
		if err := change.Normalize(); !errors.Is(err, ErrInvalidBlocklistChange) {
			t.Fatalf("%q was accepted", value)
		}
	}
	ok := BlocklistChange{Action: "block", AndroidPackage: " com.google.android.youtube "}
	if err := ok.Normalize(); err != nil || ok.AndroidPackage != "com.google.android.youtube" {
		t.Fatalf("%+v %v", ok, err)
	}
}
