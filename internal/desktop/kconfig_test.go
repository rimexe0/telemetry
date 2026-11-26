package desktop

import "testing"

// The owner's kwinrc on catchy (Plasma 6.8), with the grayscale filter
// chosen in System Settings → Accessibility → Color Blindness Correction.
const kwinrc = `[Desktops]
Id_1=f384fa92
Number=1

[Effect-colorblindnesscorrection]
Mode=3

[Plugins]
blurEnabled=true
colorblindnesscorrectionEnabled[$i]=false

[Tiling][f384fa92][]
padding=4
tiles={"layoutDirection":"horizontal","tiles":[{"width":0.25}]}
`

func TestKConfigValueReadsOneGroupsKey(t *testing.T) {
	if mode, ok := kconfigValue(kwinrc, "Effect-colorblindnesscorrection", "Mode"); !ok || mode != "3" {
		t.Fatalf("mode = %q %v", mode, ok)
	}
	if enabled, ok := kconfigValue(kwinrc, "Plugins", "colorblindnesscorrectionEnabled"); !ok || enabled != "false" {
		t.Fatalf("a key with flags = %q %v", enabled, ok)
	}
	if _, ok := kconfigValue(kwinrc, "Plugins", "Mode"); ok {
		t.Fatal("a key of another group was read")
	}
	if _, ok := kconfigValue(kwinrc, "Tiling", "padding"); ok {
		t.Fatal("a nested group was taken for its parent")
	}
}
