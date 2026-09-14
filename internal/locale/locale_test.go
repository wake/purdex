package locale

import (
	"os"
	"testing"
)

// setLocaleEnv sets LC_ALL / LC_CTYPE / LANG for the test; an empty value
// means "unset". t.Setenv registers the restore, os.Unsetenv then clears it.
func setLocaleEnv(t *testing.T, lcAll, lcCtype, lang string) {
	t.Helper()
	for _, kv := range []struct{ k, v string }{
		{"LC_ALL", lcAll},
		{"LC_CTYPE", lcCtype},
		{"LANG", lang},
	} {
		t.Setenv(kv.k, kv.v)
		if kv.v == "" {
			os.Unsetenv(kv.k)
		}
	}
}

func TestEnsureUTF8(t *testing.T) {
	cases := []struct {
		name       string
		lcAll      string
		lcCtype    string
		lang       string
		wantAction Action
		wantValue  string
		// env after the call
		wantLCAll   string
		wantLCCtype string
		wantLang    string
	}{
		{
			name:       "nothing set",
			wantAction: Set, wantValue: DefaultLocale,
			wantLang: DefaultLocale,
		},
		{
			name:       "LANG utf8",
			lang:       "en_US.UTF-8",
			wantAction: Kept, wantValue: "en_US.UTF-8",
			wantLang: "en_US.UTF-8",
		},
		{
			name:       "LANG lowercase utf8",
			lang:       "zh_TW.utf8",
			wantAction: Kept, wantValue: "zh_TW.utf8",
			wantLang: "zh_TW.utf8",
		},
		{
			name:       "LC_CTYPE macOS form",
			lcCtype:    "UTF-8",
			wantAction: Kept, wantValue: "UTF-8",
			wantLCCtype: "UTF-8",
		},
		{
			name:       "LC_ALL wins over LANG=C",
			lcAll:      "en_US.UTF-8",
			lang:       "C",
			wantAction: Kept, wantValue: "en_US.UTF-8",
			wantLCAll: "en_US.UTF-8", wantLang: "C",
		},
		{
			name:       "LC_ALL=C explicit",
			lcAll:      "C",
			lang:       "en_US.UTF-8",
			wantAction: Warned, wantValue: "C",
			wantLCAll: "C", wantLang: "en_US.UTF-8",
		},
		{
			name:       "LANG=C only",
			lang:       "C",
			wantAction: Warned, wantValue: "C",
			wantLang: "C",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setLocaleEnv(t, tc.lcAll, tc.lcCtype, tc.lang)

			got := EnsureUTF8()

			if got.Action != tc.wantAction {
				t.Errorf("Action = %v, want %v", got.Action, tc.wantAction)
			}
			if got.Value != tc.wantValue {
				t.Errorf("Value = %q, want %q", got.Value, tc.wantValue)
			}
			for _, kv := range []struct{ k, want string }{
				{"LC_ALL", tc.wantLCAll},
				{"LC_CTYPE", tc.wantLCCtype},
				{"LANG", tc.wantLang},
			} {
				if v := os.Getenv(kv.k); v != kv.want {
					t.Errorf("env %s = %q after call, want %q", kv.k, v, kv.want)
				}
			}
		})
	}
}

func TestActionString(t *testing.T) {
	cases := map[Action]string{Kept: "kept", Set: "set", Warned: "warned"}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", int(a), got, want)
		}
	}
	if got := Action(99).String(); got != "unknown" {
		t.Errorf("Action(99).String() = %q, want %q", got, "unknown")
	}
}

func TestIsUTF8(t *testing.T) {
	cases := map[string]bool{
		"en_US.UTF-8": true,
		"zh_TW.utf8":  true,
		"UTF-8":       true,
		"C.utf-8":     true,
		"C":           false,
		"POSIX":       false,
		"en_US":       false,
		"":            false,
	}
	for v, want := range cases {
		if got := isUTF8(v); got != want {
			t.Errorf("isUTF8(%q) = %v, want %v", v, got, want)
		}
	}
}
