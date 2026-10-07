package plugin

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/wake/purdex/internal/team"
)

var update = flag.Bool("update", false, "rewrite purdex/hooks/prompts.js from internal/team/relay_prompts.go")

// promptsJS is the generated file, relative to this package's directory
// (where go test runs).
const promptsJS = "purdex/hooks/prompts.js"

// renderPromptsJS renders the mod's built-in copy of the relay prompts
// from the daemon's one source (plan v3 P9a "One source for the
// defaults"): a header line, then one `export const` per value as JSON,
// which is a valid JS expression. The encoder does not escape <, > and &
// (SetEscapeHTML(false)), and escapes U+2028 / U+2029 regardless.
func renderPromptsJS(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("// GENERATED from internal/team/relay_prompts.go by: go test ./cmd/pdx/plugin/ -run TestPromptsJS -update — do not edit.\n")
	for _, c := range []struct {
		name string
		v    any
	}{
		{"DEFAULT_BODIES", team.DefaultRelayPromptBodies},
		{"FIXED", team.RelayPromptFixedParts},
		{"VARIABLES", team.RelayPromptVariables},
	} {
		b.WriteString("export const " + c.name + " = ")
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(c.v); err != nil { // Encode ends the line
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

// Spec §8.8: the mod keeps an identical built-in copy of the defaults for
// its fallback, and a test pins that the two copies are equal — byte for
// byte, so a drift on either side (a Go default edited without -update, or
// prompts.js edited by hand) is red.
func TestPromptsJS_IsGeneratedFromTheDaemonsDefaults(t *testing.T) {
	want := renderPromptsJS(t)
	if *update {
		if err := os.WriteFile(promptsJS, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(promptsJS)
	if err != nil {
		t.Fatalf("%v (generate it: go test ./cmd/pdx/plugin/ -run TestPromptsJS -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not what internal/team/relay_prompts.go renders; run: go test ./cmd/pdx/plugin/ -run TestPromptsJS -update\ngot:\n%s\nwant:\n%s", promptsJS, got, want)
	}
}
