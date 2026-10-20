package wire

import (
	"encoding/json"
	"testing"
)

func TestCoreKindValidate(t *testing.T) {
	s := func(v string) *string { return &v }
	ok := Facts{V: FactsProto, Derived: Derived{Zone: ZoneNormal}}
	for _, k := range []*string{nil, s("p"), s("e")} {
		f := ok
		f.CPU.Cores = []Core{{Kind: k}}
		if err := f.Validate(); err != nil {
			t.Errorf("kind %v: %v", k, err)
		}
	}
	for _, bad := range []string{"", "P", "E", "perf", "x"} {
		f := ok
		f.CPU.Cores = []Core{{Kind: s("p")}, {Kind: s(bad)}}
		if err := f.Validate(); err == nil {
			t.Errorf("kind %q must be refused", bad)
		}
	}
}

// A record written before these fields decodes with them nil; one with them round-trips.
func TestCPUNewFieldsAreNullable(t *testing.T) {
	old := `{"ncpu":4,"load1":1,"load5":2,"idle_pct":50}`
	c, err := DecodeLenient[CPU]([]byte(old))
	if err != nil || c.Load15 != nil || c.UserPct != nil || c.SystemPct != nil || c.Cores != nil {
		t.Fatalf("%+v %v", c, err)
	}
	b, _ := json.Marshal(c)
	if string(b) != old { // nothing new is written for a record that has none of it
		t.Errorf("re-encoded %s", b)
	}
	k := "p"
	full := CPU{NCPU: 2, Load15: new(float64), UserPct: new(float64), SystemPct: new(float64),
		Cores: []Core{{UserPct: new(float64), Kind: &k}, {}}}
	b, _ = json.Marshal(full)
	back, err := DecodeLenient[CPU](b)
	if err != nil || back.Load15 == nil || len(back.Cores) != 2 || *back.Cores[0].Kind != "p" || back.Cores[1].Kind != nil {
		t.Fatalf("%s: %+v %v", b, back, err)
	}
}
