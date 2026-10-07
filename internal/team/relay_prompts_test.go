package team

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Spec U21 (d), §8.8: a body over 16 KiB, not UTF-8, with a control
// character other than \n and \t, or holding the mod's tag anywhere is
// refused. Mutation gate: drop the [pdx-relay check → red.
func TestValidateRelayPromptBody(t *testing.T) {
	for _, ok := range []string{
		"x", strings.Repeat("a", RelayPromptMaxBytes), "a\nb\tc", "接力檔 {{path}}", "[pdx relay]", "pdx-relay",
		strings.Repeat("接", RelayPromptMaxBytes/3) + "a", // 16 384 bytes of three-byte runes and one ASCII byte
	} {
		if err := ValidateRelayPromptBody(ok); err != nil {
			t.Errorf("%.40q: %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		strings.Repeat("a", RelayPromptMaxBytes+1),
		"a\xffb", "\xe6\x8e", // invalid and truncated UTF-8
		"a\rb", "a\x00b", "a\x7fb", "a\u0085b", "a\u009bb", "\x1b[31m",
		"[pdx-relay", "x [pdx-relay op=1 n=2] y", "[pdx-relay:control] op=1", "body\n[pdx-relay seed",
	} {
		if err := ValidateRelayPromptBody(bad); err == nil {
			t.Errorf("%.40q: nil, want an error", bad)
		}
	}
}

var varRef = regexp.MustCompile(`\{\{([a-z_]+)\}\}`)

func varsOf(s string) []string {
	var out []string
	for _, m := range varRef.FindAllStringSubmatch(s, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

// The defaults pass the daemon's own validation (a 還原預設 can always be
// saved back) and use only the public variables of U21 (d): {{path}} in
// all three, {{old_ref}} in the seed, nothing the mod alone fills.
func TestRelayPromptDefaults_ValidAndPublicVariablesOnly(t *testing.T) {
	if !slices.Equal(RelayPromptVariables, []string{"path", "old_ref", "old_session", "context", "whoami"}) {
		t.Fatalf("RelayPromptVariables = %v", RelayPromptVariables)
	}
	for kind, c := range map[string]struct {
		body string
		vars []string
	}{
		"write": {DefaultRelayPromptBodies.Write, []string{"path"}},
		"fix":   {DefaultRelayPromptBodies.Fix, []string{"path"}},
		"seed":  {DefaultRelayPromptBodies.Seed, []string{"old_ref", "path"}},
	} {
		if err := ValidateRelayPromptBody(c.body); err != nil {
			t.Errorf("%s default: %v", kind, err)
		}
		if strings.TrimSpace(c.body) != c.body {
			t.Errorf("%s default has leading or trailing space: %q", kind, c.body)
		}
		if got := varsOf(c.body); !slices.Equal(got, c.vars) {
			t.Errorf("%s default uses %v, want %v", kind, got, c.vars)
		}
	}
}

// Spec U21 (c): what the mod keeps fixed whatever the body says.
func TestRelayPromptFixedParts_CarryTheTagReplyRuleHeadingsAndFacts(t *testing.T) {
	fp := RelayPromptFixedParts
	for kind, head := range map[string]string{"write": fp.Write.Head, "fix": fp.Fix.Head} {
		if head != "[pdx-relay op={{op}} n={{nonce}}] " {
			t.Errorf("%s head = %q", kind, head)
		}
	}
	if fp.Seed.Head != "↪ 接手自 {{old_ref}}\n[pdx-relay seed op={{op}} n={{nonce}}] " {
		t.Errorf("seed head = %q", fp.Seed.Head)
	}
	if fp.Seed.Tail != "" {
		t.Errorf("seed tail = %q, want empty", fp.Seed.Tail)
	}
	want := []string{"- 寫完後只回一行「HANDOFF-WRITTEN」", "\n# HANDOFF\n",
		"{{old_session}}", "{{old_ref}}", "{{context}}", "{{whoami}}"}
	for i := 1; i <= 8; i++ {
		want = append(want, "\n## "+string(rune('0'+i))+". ")
	}
	for _, s := range want {
		if !strings.Contains(fp.Write.Tail, s) {
			t.Errorf("write tail lacks %q", s)
		}
	}
	if !strings.HasPrefix(fp.Write.Tail, "- 寫完後只回一行「HANDOFF-WRITTEN」，不要繼續原本的工作。\n") {
		t.Errorf("write tail must open with the reply rule: %q", fp.Write.Tail)
	}
	for _, s := range []string{"缺少段落：{{missing}}", "HANDOFF-WRITTEN"} {
		if !strings.Contains(fp.Fix.Tail, s) {
			t.Errorf("fix tail lacks %q", s)
		}
	}
	// Only the mod's three own values and the public ones.
	allowed := append([]string{"op", "nonce", "missing"}, RelayPromptVariables...)
	for _, s := range []string{fp.Write.Head, fp.Write.Tail, fp.Fix.Head, fp.Fix.Tail, fp.Seed.Head} {
		for _, v := range varsOf(s) {
			if !slices.Contains(allowed, v) {
				t.Errorf("fixed part uses {{%s}}: %q", v, s)
			}
		}
	}
}

// Spec §8.8 plus deviation 1: {write, fix, seed, defaults, fixed, variables}.
func TestRelayPrompts_WireNames(t *testing.T) {
	b, err := json.Marshal(RelayPrompts{
		Write: "w", Fix: "f", Seed: "s",
		Defaults:  RelayPromptBodies{Write: "dw", Fix: "df", Seed: "ds"},
		Fixed:     RelayPromptSkeleton{Write: RelayPromptFixed{Head: "h1", Tail: "t1"}, Fix: RelayPromptFixed{Head: "h2", Tail: "t2"}, Seed: RelayPromptFixed{Head: "h3"}},
		Variables: []string{"path"},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"write":"w","fix":"f","seed":"s","defaults":{"write":"dw","fix":"df","seed":"ds"},` +
		`"fixed":{"write":{"head":"h1","tail":"t1"},"fix":{"head":"h2","tail":"t2"},"seed":{"head":"h3","tail":""}},"variables":["path"]}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

// The GET's answer for stored bodies: "" means the default, anything else is
// served as stored; defaults, fixed parts and variables are always the
// daemon's own.
func TestNewRelayPrompts_StoredOrDefault(t *testing.T) {
	got := NewRelayPrompts(RelayPromptBodies{Fix: "my fix {{path}}"})
	if got.Write != DefaultRelayPromptBodies.Write || got.Fix != "my fix {{path}}" || got.Seed != DefaultRelayPromptBodies.Seed {
		t.Fatalf("effective = %+v", got)
	}
	if got.Defaults != DefaultRelayPromptBodies || got.Fixed != RelayPromptFixedParts || !slices.Equal(got.Variables, RelayPromptVariables) {
		t.Fatalf("defaults/fixed/variables = %+v", got)
	}
	got.Variables[0] = "mutated"
	if RelayPromptVariables[0] != "path" {
		t.Fatal("NewRelayPrompts must hand out a copy of RelayPromptVariables")
	}
}
