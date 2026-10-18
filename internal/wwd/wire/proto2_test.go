package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// Spec §13.1 (WWD-8 8a-c): proto 2 teaches every reader the new fields and the new route; writers keep sending the v1 shape
// until every pool host reports proto >= 2. These tests pin both halves of that.

func TestProtoConstants(t *testing.T) {
	if Proto != 2 {
		t.Fatalf("Proto = %d: the highest version a reader understands is 2", Proto)
	}
	if WriteProto != 1 {
		t.Fatalf("WriteProto = %d: writers send v 1 until every host reports proto >= 2 (a later PR raises it)", WriteProto)
	}
}

func TestFactsVersionRange(t *testing.T) {
	good := fixture(t, "facts_darwin.json")
	for _, c := range []struct {
		v  string
		ok bool
	}{{`"v": 0,`, false}, {`"v": 1,`, true}, {`"v": 2,`, true}, {`"v": 3,`, false}} {
		js := strings.Replace(string(good), `"v": 1,`, c.v, 1)
		_, err := Decode[Facts]([]byte(js))
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.v, err, c.ok)
		}
	}
}

func TestInfoProtoRange(t *testing.T) {
	good := fixture(t, "info.json")
	for _, c := range []struct {
		p  string
		ok bool
	}{{`"proto": 0,`, false}, {`"proto": 1,`, true}, {`"proto": 2,`, true}, {`"proto": 3,`, false}} {
		js := strings.Replace(string(good), `"proto": 1,`, c.p, 1)
		_, err := Decode[Info]([]byte(js))
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.p, err, c.ok)
		}
	}
}

// New reader, old payload: every v1 fixture still decodes, with the new fields absent (unknown).
func TestNewReaderTakesAV1Payload(t *testing.T) {
	for _, name := range []string{"facts_darwin.json", "facts_linux.json", "facts_minute_stats.json"} {
		f := roundTrip[Facts](t, name)
		if f.Mem.DecompressRate != nil || f.Queue != nil {
			t.Errorf("%s: the new fields must read as absent: %+v %+v", name, f.Mem.DecompressRate, f.Queue)
		}
		for _, e := range f.Exception {
			if e.Allow != nil || len(e.Flags) != 0 {
				t.Errorf("%s: exception %s has hold fields it never had", name, e.Name)
			}
		}
	}
	e := roundTrip[Exception](t, "exception.json")
	if e.Allow != nil || len(e.Flags) != 0 {
		t.Errorf("an exception from before reads as before: %+v", e)
	}
}

// New reader, new payload.
func TestFactsV2RoundTrip(t *testing.T) {
	f := roundTrip[Facts](t, "facts_v2_darwin.json")
	if f.V != 2 || f.Mem.DecompressRate == nil || *f.Mem.DecompressRate != 81250.5 {
		t.Fatalf("decompress_rate: %+v", f.Mem)
	}
	if f.Queue == nil || f.Queue.Queued != 3 {
		t.Fatalf("queue: %+v", f.Queue)
	}
	var hold *FactExc
	for i := range f.Exception {
		if f.Exception[i].Name == "release-hold" {
			hold = &f.Exception[i]
		}
	}
	if hold == nil || hold.Allow == nil || len(hold.Allow.Classes) != 1 || hold.Allow.Classes[0] != ClassRelease ||
		len(hold.Flags) != 2 || hold.Flags[0] != HoldFlagRelease || hold.Flags[1] != HoldFlagExpectPressure {
		t.Fatalf("hold in facts: %+v", hold)
	}
}

// Old reader, writer not yet gated: a writer that fills none of the new fields produces exactly the v1 shape, so a reader that
// does not know them (strict decode) is never shown one.
func TestUnsetNewFieldsAreNotWritten(t *testing.T) {
	f, err := Decode[Facts](fixture(t, "facts_darwin.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(f)
	for _, k := range []string{"decompress_rate", `"queue"`, `"allow"`, `"flags"`} {
		if strings.Contains(string(b), k) {
			t.Errorf("a facts record without the new fields wrote %s: %s", k, b)
		}
	}
	e, err := Decode[Exception](fixture(t, "exception.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(e)
	if strings.Contains(string(b), "allow") || strings.Contains(string(b), "flags") {
		t.Errorf("an exception without the new fields wrote them: %s", b)
	}
}

// Why the writers are gated: a reader without the fields rejects them. v1Mem is mem as proto 1 readers know it.
func TestAnOldReaderRejectsTheNewFields(t *testing.T) {
	type v1Mem struct {
		TotalMB      int      `json:"total_mb"`
		AvailableMB  int      `json:"available_mb"`
		Pressure     int      `json:"pressure"`
		PageOutRate  *float64 `json:"page_out_rate"`
		SwapOutRate  *float64 `json:"swap_out_rate"`
		SwapUsedMB   *int     `json:"swap_used_mb"`
		CompressorMB *int     `json:"compressor_mb"`
		PSI          *PSI     `json:"psi"`
	}
	js := `{"total_mb":1,"available_mb":1,"pressure":1,"page_out_rate":null,"swap_out_rate":null,"swap_used_mb":null,"compressor_mb":null,"psi":null,"decompress_rate":5}`
	if _, err := Decode[v1Mem]([]byte(js)); err == nil {
		t.Fatal("a v1 reader took an unknown field: the gate would not be needed")
	}
}

func TestHoldFieldsValidate(t *testing.T) {
	base := `{"name":"h","until":"2026-10-18T06:00:00+08:00","pid":null,"holds":["heavy"],`
	for _, c := range []struct {
		name, tail string
		ok         bool
	}{
		{"classes and repos", `"allow":{"classes":["release","merge","dev"],"repos":["purdex","at-inwin-plugin"]}}`, true},
		{"both empty: admit none", `"allow":{"classes":[],"repos":[]}}`, true},
		{"flags only", `"flags":["release"]}`, true},
		{"bad class", `"allow":{"classes":["urgent"],"repos":[]}}`, false},
		{"bad repo", `"allow":{"classes":[],"repos":["a b"]}}`, false},
		{"empty repo", `"allow":{"classes":[],"repos":[""]}}`, false},
		{"bad flag", `"flags":["panic"]}`, false},
		{"repeated flag", `"flags":["release","release"]}`, false},
		{"unknown field", `"allows":{}}`, false},
	} {
		_, err := Decode[Exception]([]byte(base + c.tail))
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
	}
	if _, err := Decode[Exception](fixture(t, "exception_hold.json")); err != nil {
		t.Errorf("hold fixture: %v", err)
	}
	roundTrip[Exception](t, "exception_hold.json")
}

func TestAlertListFixture(t *testing.T) {
	l := roundTrip[AlertList](t, "alert_list.json")
	if l.Host != "mlab" || len(l.Alerts) != 2 || l.Alerts[0].ClearedAt != nil || l.Alerts[1].ClearedAt == nil {
		t.Fatalf("%+v", l)
	}
	if l.Alerts[0].Kind != AlertDiskLow || l.Alerts[0].Level != AlertPush || l.Alerts[1].Level != AlertDigest {
		t.Fatalf("%+v", l.Alerts)
	}
}

func TestAlertListValidates(t *testing.T) {
	a := `{"key":"k","kind":"disk_low","level":"push","since":"2026-10-19T04:00:00+08:00","last":"2026-10-19T04:10:00+08:00","cleared_at":null}`
	for _, c := range []struct {
		name, js string
		ok       bool
	}{
		{"good", `{"host":"mlab","alerts":[` + a + `]}`, true},
		{"empty list", `{"host":"mlab","alerts":[]}`, true},
		{"no host", `{"host":"","alerts":[]}`, false},
		{"bad kind", `{"host":"mlab","alerts":[` + strings.Replace(a, "disk_low", "meltdown", 1) + `]}`, false},
		{"bad level", `{"host":"mlab","alerts":[` + strings.Replace(a, `"push"`, `"loud"`, 1) + `]}`, false},
		{"empty key", `{"host":"mlab","alerts":[` + strings.Replace(a, `"key":"k"`, `"key":""`, 1) + `]}`, false},
		{"last before since", `{"host":"mlab","alerts":[` + strings.Replace(a, `"last":"2026-10-19T04:10:00+08:00"`, `"last":"2026-10-19T03:00:00+08:00"`, 1) + `]}`, false},
		{"unknown field", `{"host":"mlab","alerts":[],"x":1}`, false},
		// cleared_at is when it stopped: not before the last time the rule still held (R1)
		{"cleared between since and last", `{"host":"mlab","alerts":[` + strings.Replace(a, `"cleared_at":null`, `"cleared_at":"2026-10-19T04:05:00+08:00"`, 1) + `]}`, false},
		{"cleared at last", `{"host":"mlab","alerts":[` + strings.Replace(a, `"cleared_at":null`, `"cleared_at":"2026-10-19T04:10:00+08:00"`, 1) + `]}`, true},
	} {
		_, err := Decode[AlertList]([]byte(c.js))
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
	}
}

func TestAlertEvent(t *testing.T) {
	e := roundTrip[Event](t, "event_alert.json")
	if e.Type != EventAlert {
		t.Fatalf("type %q", e.Type)
	}
	v, err := e.DecodeData()
	if err != nil {
		t.Fatal(err)
	}
	if ev, ok := v.(AlertEvent); !ok || ev.Host != "mlab" {
		t.Fatalf("%#v", v)
	}
	if _, err := Decode[Event]([]byte(`{"type":"alert","data":{"host":"mlab","alerts":[]}}`)); err == nil {
		t.Fatal("the alert event is only a hint: a list in it must be refused")
	}
	// it must say which host's list to read again (R1)
	for _, js := range []string{`{"type":"alert","data":{"host":""}}`, `{"type":"alert","data":{}}`} {
		if _, err := Decode[Event]([]byte(js)); err == nil {
			t.Fatalf("an alert event without a host was accepted: %s", js)
		}
	}
}

func TestQueueValidates(t *testing.T) {
	js := strings.Replace(string(fixture(t, "facts_v2_darwin.json")), `"queued": 3`, `"queued": -1`, 1)
	if _, err := Decode[Facts]([]byte(js)); err == nil {
		t.Fatal("a negative queue length was accepted")
	}
	js = strings.Replace(string(fixture(t, "facts_v2_darwin.json")), `"decompress_rate": 81250.5`, `"decompress_rate": -1`, 1)
	if _, err := Decode[Facts]([]byte(js)); err == nil {
		t.Fatal("a negative decompress_rate was accepted")
	}
}
