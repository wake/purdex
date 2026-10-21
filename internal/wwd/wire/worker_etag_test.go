package wire

import (
	"regexp"
	"testing"
)

func TestWorkerEtagIsOfTheValuesNotTheSpelling(t *testing.T) {
	a, err := Decode[WorkerSettings]([]byte(`{"enabled":true,"slots":{"heavy":1,"light":2},"owner_rule":"hid<3m","disk_min_gb":10}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Decode[WorkerSettings]([]byte(`{"disk_min_gb":10.0,"owner_rule":"hid<3m","slots":{"light":2,"heavy":1},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if a.Etag() != b.Etag() {
		t.Errorf("field order or number spelling changed the etag: %s %s", a.Etag(), b.Etag())
	}
	// a floor left out is the default floor in force, so it is the same setting as one that names the default
	c := a
	c.DiskMinGB = nil
	if c.Etag() != a.Etag() {
		t.Errorf("an absent floor and the default floor are one setting: %s %s", c.Etag(), a.Etag())
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(a.Etag()) {
		t.Errorf("etag %q is not 16 hex digits", a.Etag())
	}
}

func TestWorkerEtagSeparatesEveryField(t *testing.T) {
	floor := 25.0
	base := WorkerSettings{Enabled: true, Slots: Slots{Heavy: 1, Light: 2}, OwnerRule: "hid<3m"}
	seen := map[string]string{base.Etag(): "base"}
	for name, w := range map[string]WorkerSettings{
		"enabled":    {Enabled: false, Slots: base.Slots, OwnerRule: base.OwnerRule},
		"heavy":      {Enabled: true, Slots: Slots{Heavy: 2, Light: 2}, OwnerRule: base.OwnerRule},
		"light":      {Enabled: true, Slots: Slots{Heavy: 1, Light: 3}, OwnerRule: base.OwnerRule},
		"owner_rule": {Enabled: true, Slots: base.Slots, OwnerRule: ""},
		"floor":      {Enabled: true, Slots: base.Slots, OwnerRule: base.OwnerRule, DiskMinGB: &floor},
		// heavy 1 light 2 and heavy 2 light 1 must not collide
		"swapped slots": {Enabled: true, Slots: Slots{Heavy: 2, Light: 1}, OwnerRule: base.OwnerRule},
	} {
		if other, dup := seen[w.Etag()]; dup {
			t.Errorf("%s has the etag of %s", name, other)
		}
		seen[w.Etag()] = name
	}
}

func TestWorkerViewAndPutDecode(t *testing.T) {
	v := WorkerSettings{Enabled: true, Slots: Slots{Heavy: 1}}.View()
	if v.Etag == "" || v.Etag != v.WorkerSettings.Etag() {
		t.Fatalf("view %+v", v)
	}
	// an old reader takes the answer as plain settings and skips the etag
	if _, err := DecodeLenient[WorkerSettings]([]byte(`{"enabled":true,"slots":{"heavy":1,"light":0},"owner_rule":"","etag":"` + v.Etag + `"}`)); err != nil {
		t.Errorf("an old client cannot read the view: %v", err)
	}
	p, err := Decode[WorkerPut]([]byte(`{"enabled":true,"slots":{"heavy":1,"light":0},"owner_rule":"","expect_etag":"abc"}`))
	if err != nil || p.ExpectEtag != "abc" || !p.Enabled {
		t.Errorf("put %+v err %v", p, err)
	}
	// a body without expect_etag is the PUT it always was; a field nobody knows is still refused
	if _, err := Decode[WorkerPut]([]byte(`{"enabled":true,"slots":{"heavy":1,"light":0},"owner_rule":""}`)); err != nil {
		t.Errorf("old body: %v", err)
	}
	if _, err := Decode[WorkerPut]([]byte(`{"enabled":true,"slots":{"heavy":1,"light":0},"owner_rule":"","zzz":1}`)); err == nil {
		t.Error("an unknown field must be refused")
	}
}
