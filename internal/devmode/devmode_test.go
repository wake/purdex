package devmode

import (
	"os"
	"testing"
)

func TestEnabled(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"unset", false, "", true},
		{"one", true, "1", true},
		{"zero", true, "0", false},
		{"false-word", true, "false", true}, // only "0" disables
		{"empty-string", true, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("PDX_DEV_MODE", c.value)
			} else {
				t.Setenv("PDX_DEV_MODE", "")
				// t.Setenv cannot unset; emulate unset via os.Unsetenv after Setenv registered the restore.
				unsetForTest(t)
			}
			if got := Enabled(); got != c.want {
				t.Fatalf("Enabled() with %q(set=%v) = %v, want %v", c.value, c.set, got, c.want)
			}
		})
	}
}

func unsetForTest(t *testing.T) {
	t.Helper()
	if err := os.Unsetenv("PDX_DEV_MODE"); err != nil {
		t.Fatal(err)
	}
}
