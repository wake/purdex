package wire

import "testing"

func TestSubmissionResponse(t *testing.T) {
	roundTrip[JobSubmissionResponse](t, "submission_response.json")
	r := roundTrip[JobSubmissionResponse](t, "submission_response_cache_hit.json")
	if r.Result == nil || !r.Result.CacheHit {
		t.Fatal("cache hit result lost")
	}
	if _, err := Decode[JobSubmissionResponse]([]byte(`{"id":"","result":null}`)); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestNestedValidation(t *testing.T) {
	badFacts := string(replaceIn(t, "facts_linux.json", `"zone": "unknown"`, `"zone": "hot"`))
	badInfo := `{"host_id":"h","alias":"a","version":"v","proto":9,"platform":"p","arch":"a","worker":{"enabled":true},"labels":[]}`
	badJob := string(replaceIn(t, "job_queued.json", `"class": "merge"`, `"class": "urgent"`))
	info := string(fixture(t, "info.json"))
	ws := `{"enabled":false,"slots":{"heavy":0,"light":0},"owner_rule":""}`
	cases := map[string]func() error{
		"pool facts": func() error {
			_, err := Decode[Pool]([]byte(`{"peers":[{"info":` + info + `,"facts":` + badFacts + `,"reachable":true}]}`))
			return err
		},
		"pool info": func() error {
			_, err := Decode[Pool]([]byte(`{"peers":[{"info":` + badInfo + `,"facts":null,"reachable":true}]}`))
			return err
		},
		"history record": func() error {
			_, err := Decode[FactsHistoryResponse]([]byte(`{"step":"1m","records":[` + badFacts + `]}`))
			return err
		},
		"hosts info": func() error {
			_, err := Decode[PoolHostsResponse]([]byte(`{"hosts":[{"info":` + badInfo + `,"facts":null,"worker":` + ws + `,"labels":[],"reachable":false}]}`))
			return err
		},
		"hosts facts": func() error {
			_, err := Decode[PoolHostsResponse]([]byte(`{"hosts":[{"info":` + info + `,"facts":` + badFacts + `,"worker":` + ws + `,"labels":[],"reachable":true}]}`))
			return err
		},
		"jobs list": func() error {
			_, err := Decode[JobsListResponse]([]byte(`{"jobs":[` + badJob + `]}`))
			return err
		},
	}
	for name, f := range cases {
		if f() == nil {
			t.Fatalf("%s: nested invalid value accepted", name)
		}
	}
}

func TestExceptionExactlyOneBound(t *testing.T) {
	until := `"2026-10-18T05:30:00+08:00"`
	for name, js := range map[string]string{
		"neither":      `{"name":"x","until":null,"pid":null,"holds":["heavy"]}`,
		"both":         `{"name":"x","until":` + until + `,"pid":5,"holds":["heavy"]}`,
		"non-positive": `{"name":"x","until":null,"pid":0,"holds":["heavy"]}`,
		"no name":      `{"name":"","until":null,"pid":5,"holds":[]}`,
	} {
		if _, err := Decode[Exception]([]byte(js)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if _, err := Decode[Exception]([]byte(`{"name":"x","until":null,"pid":5,"holds":[]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestClaimRequestNeedsWorker(t *testing.T) {
	if _, err := Decode[ClaimRequest]([]byte(`{}`)); err == nil {
		t.Fatal("empty claim request accepted")
	}
}
