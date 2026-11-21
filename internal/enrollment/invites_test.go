package enrollment

import "testing"

func TestNormalizeCode(t *testing.T) {
	for input, want := range map[string]string{
		"K7QM-3XPD": "K7QM3XPD",
		"k7qm 3xpd": "K7QM3XPD",
		"O1IL-0000": "0111" + "0000",
	} {
		got, ok := NormalizeCode(input)
		if !ok || got != want {
			t.Fatalf("NormalizeCode(%q) = %q, %v", input, got, ok)
		}
	}
	for _, bad := range []string{"", "K7QM", "K7QM-3XPDX", "K7QM-3XPU", "K7QM_3XPD"} {
		if _, ok := NormalizeCode(bad); ok {
			t.Fatalf("NormalizeCode(%q) accepted", bad)
		}
	}
}

func TestNewCodeIsNormalized(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := newCode()
		if err != nil {
			t.Fatal(err)
		}
		got, ok := NormalizeCode(FormatCode(code))
		if !ok || got != code {
			t.Fatalf("code %q does not survive formatting: %q %v", code, got, ok)
		}
		seen[code] = true
	}
	if len(seen) < 199 {
		t.Fatalf("codes repeat: %d distinct of 200", len(seen))
	}
}
