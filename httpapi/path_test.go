package httpapi

import "testing"

func TestFormBucketsUnknownExtensions(t *testing.T) {
	cases := map[string]string{
		"integration.pdb":   "pdb",
		"integration.pd_":   "pd_",
		"KERNEL32.DL_":      "dl_", // case-folded
		"app.EXE":           "exe",
		"bundle.src.zip":    "zip",
		"_.debug":           "other", // native placeholder: not its own label
		"no-extension":      "other",
		"attacker.aAaAaA42": "other", // arbitrary client input collapses to "other"
	}

	for leaf, want := range cases {
		if got := form(leaf); got != want {
			t.Errorf("form(%q) = %q, want %q", leaf, got, want)
		}
	}
}
