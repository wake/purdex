package profilehash

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The shared fixture is pinned on both sides: spa/src/lib/profile/hash.fixture.test.ts runs hash.ts over it, this runs the port.
func TestSum_MatchesTheSharedFixture(t *testing.T) {
	raw, err := os.ReadFile("../../spa/src/lib/profile/__fixtures__/canonical-hash.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct{ Name, JSON, Hash string }
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 20 {
		t.Fatalf("only %d cases", len(f.Cases))
	}
	for _, c := range f.Cases {
		got, err := Sum([]byte(c.JSON))
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if got != c.Hash {
			t.Errorf("%s: got %s, want %s", c.Name, got, c.Hash)
		}
	}
}

func TestCanonical_Forms(t *testing.T) {
	for in, want := range map[string]string{
		`{"b":1,"a":[2,1]}`:                `{"a":[2,1],"b":1}`,
		`[-0,1e21,1e-7,0.000001,100,1.50]`: `[0,1e+21,1e-7,0.000001,100,1.5]`,
		`"\u007f \u0001\/"`:                "\"\x7f \\u0001/\"",
		`{"～":1,"😀":2}`:                    "{\"\U0001F600\":2,\"～\":1}",
		`{"a":1,"a":2}`:                    `{"a":2}`,
		` { "a" : [ ] } `:                  `{"a":[]}`,
		`123456789012345680000`:            `123456789012345680000`,
		`5e-324`:                           `5e-324`,
		`1.7976931348623157e308`:           `1.7976931348623157e+308`,
		`{"10":1,"9":2,"a":3}`:             `{"10":1,"9":2,"a":3}`,
	} {
		got, err := Canonical([]byte(in))
		if err != nil || got != want {
			t.Errorf("%s: got %q err %v, want %q", in, got, err, want)
		}
	}
}

// Anything the port cannot reproduce exactly is an error, never a hash.
func TestCanonical_Refuses(t *testing.T) {
	deep := strings.Repeat("[", maxDepth+2) + strings.Repeat("]", maxDepth+2)
	for name, in := range map[string]string{
		"lone high surrogate": `"\ud800"`,
		"lone low surrogate":  `"\udc00"`,
		"high then non-low":   `"\ud800A"`,
		"invalid utf-8":       "\"\xff\"",
		"number beyond float": `1e999`,
		"empty":               ``,
		"trailing data":       `{} x`,
		"leading zero":        `01`,
		"bare minus":          `-`,
		"trailing comma":      `[1,]`,
		"single quotes":       `{'a':1}`,
		"raw control in text": "\"a\nb\"",
		"unterminated string": `"abc`,
		"too deep":            deep,
		"nan":                 `NaN`,
		"missing colon":       `{"a" 1}`,
	} {
		if got, err := Canonical([]byte(in)); err == nil {
			t.Errorf("%s: accepted as %q", name, got)
		}
	}
}
