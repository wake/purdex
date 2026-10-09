package push

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

var tok64 = strings.Repeat("ab12cd34", 8) // 64 hex chars

func good() DeviceRequest {
	return DeviceRequest{
		Token: tok64, BundleID: "tw.protype.purdex", Env: "sandbox", Platform: "ios",
		DeviceName: "iPhone 8", HostLabel: "mlab", Locale: "zh-TW",
		Prefs: Prefs{Tabs: []string{"c1", "c2"}},
	}
}

func TestValidate_AGoodRequestPasses(t *testing.T) {
	r := good()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_TokenRules(t *testing.T) {
	for name, tc := range map[string]struct {
		token string
		ok    bool
	}{
		"63 hex":    {strings.Repeat("a", 63), false},
		"64 hex":    {strings.Repeat("a", 64), true},
		"200 hex":   {strings.Repeat("A", 200), true},
		"201 hex":   {strings.Repeat("a", 201), false},
		"non-hex":   {strings.Repeat("g", 64), false},
		"has space": {strings.Repeat("a", 63) + " ", false},
		"empty":     {"", false},
		"upper":     {strings.Repeat("AB", 32), true},
	} {
		t.Run(name, func(t *testing.T) {
			r := good()
			r.Token = tc.token
			if err := r.Validate(); (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestValidate_TokenIsLowercased(t *testing.T) {
	r := good()
	r.Token = strings.ToUpper(tok64)
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if r.Token != tok64 {
		t.Fatalf("token not lowercased: %q", r.Token)
	}
}

func TestValidate_FieldRules(t *testing.T) {
	for name, mut := range map[string]func(*DeviceRequest){
		"other bundle":     func(r *DeviceRequest) { r.BundleID = "com.example.app" },
		"empty bundle":     func(r *DeviceRequest) { r.BundleID = "" },
		"bad env":          func(r *DeviceRequest) { r.Env = "staging" },
		"bad platform":     func(r *DeviceRequest) { r.Platform = "android" },
		"long device name": func(r *DeviceRequest) { r.DeviceName = strings.Repeat("字", 65) },
		"long host label":  func(r *DeviceRequest) { r.HostLabel = strings.Repeat("x", 33) },
		"control in name":  func(r *DeviceRequest) { r.DeviceName = "a\nb" },
		"control in label": func(r *DeviceRequest) { r.HostLabel = "a\x00b" },
		"too many tabs":    func(r *DeviceRequest) { r.Prefs.Tabs = make([]string, 501) },
		"empty tab code":   func(r *DeviceRequest) { r.Prefs.Tabs = []string{""} },
		"long tab code":    func(r *DeviceRequest) { r.Prefs.Tabs = []string{strings.Repeat("c", 65)} },
		"too many agents":  func(r *DeviceRequest) { r.Prefs.Agents = manyAgents(33) },
		"too many events":  func(r *DeviceRequest) { r.Prefs.Agents = map[string]AgentPrefs{"cc": {Events: manyEvents(65)}} },
		"empty agent type": func(r *DeviceRequest) { r.Prefs.Agents = map[string]AgentPrefs{"": {}} },
		"long event name": func(r *DeviceRequest) {
			r.Prefs.Agents = map[string]AgentPrefs{"cc": {Events: map[string]bool{strings.Repeat("e", 65): true}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := good()
			mut(&r)
			if err := r.Validate(); err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
	// the limits themselves are fine
	r := good()
	r.DeviceName = strings.Repeat("字", 64)
	r.HostLabel = strings.Repeat("x", 32)
	r.Prefs.Tabs = make([]string, 500)
	for i := range r.Prefs.Tabs {
		r.Prefs.Tabs[i] = "c"
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_LocaleFallsBackToZhTW(t *testing.T) {
	for in, want := range map[string]string{"zh-TW": "zh-TW", "en": "en", "fr": "zh-TW", "": "zh-TW", "EN": "zh-TW"} {
		r := good()
		r.Locale = in
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		if r.Locale != want {
			t.Fatalf("locale %q -> %q, want %q", in, r.Locale, want)
		}
	}
}

func manyAgents(n int) map[string]AgentPrefs {
	m := map[string]AgentPrefs{}
	for i := 0; i < n; i++ {
		m[strings.Repeat("a", 1)+string(rune('A'+i%26))+string(rune('a'+i/26))] = AgentPrefs{}
	}
	return m
}

func manyEvents(n int) map[string]bool {
	m := map[string]bool{}
	for i := 0; i < n; i++ {
		m[strings.Repeat("e", 1)+string(rune('A'+i%26))+string(rune('a'+i/26))] = true
	}
	return m
}

func TestDeviceID_IsStableLowercaseInsensitiveAndShort(t *testing.T) {
	a, b := DeviceID(tok64), DeviceID(strings.ToUpper(tok64))
	if a != b {
		t.Fatalf("not case-insensitive: %s vs %s", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("len = %d", len(a))
	}
	if a != DeviceID(tok64) {
		t.Fatal("not stable")
	}
	if a == DeviceID(strings.Repeat("c", 64)) {
		t.Fatal("different tokens collide")
	}
	// the first 16 hex chars of SHA-256 of the lowercased token: the id the phone can compute for itself
	sum := sha256.Sum256([]byte(tok64))
	if want := hex.EncodeToString(sum[:])[:16]; a != want {
		t.Fatalf("id = %s, want %s", a, want)
	}
}

func TestMaskToken(t *testing.T) {
	if got := MaskToken(tok64); got != "ab12cd34…cd34" {
		t.Fatalf("mask = %q", got)
	}
	if got := MaskToken("abc"); strings.Contains(got, "abc") && len(got) > 3 {
		t.Fatalf("a short token must not be echoed: %q", got)
	}
	if got := MaskToken(""); got != "" {
		t.Fatalf("mask(empty) = %q", got)
	}
}

func TestDeviceView_NeverCarriesTheFullToken(t *testing.T) {
	d := Device{DeviceID: DeviceID(tok64), Token: tok64, Env: "sandbox", Prefs: Prefs{Tabs: []string{"a", "b", "c"}}}
	v := d.View()
	if v.TabsCount != 3 || v.Token != "ab12cd34…cd34" {
		t.Fatalf("view = %+v", v)
	}
}
