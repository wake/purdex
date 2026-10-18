package wire

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

var usageT0 = time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)

func goodUsage() JobUsage {
	yes, no := true, false
	return JobUsage{
		JobID: "j_01JC", Host: "air26", Repo: "purdex", Task: "go-test", Slot: SlotHeavy, Class: ClassDev,
		Source: UsageQueue, Result: StateSucceeded,
		StartedAt: usageT0, EndedAt: usageT0.Add(90 * time.Second), DurationS: 90,
		CPUSeconds: 270, CPUSource: CPUFromRusage, AvgCores: 3,
		PeakCores: 5.5, PeakCoresMeasure: MeasureSampled,
		MemPeakMB: 2100, MemPeakMeasure: MeasureSampled,
		JobsAtStart: 1, JobsPeak: 3, ZoneAtStart: ZoneNormal, ZoneWorst: ZoneWarning,
		OwnerAtStart: &no, OwnerSeen: &yes,
	}
}

func TestJobUsageValidates(t *testing.T) {
	if err := goodUsage().Validate(); err != nil {
		t.Fatal(err)
	}
	// a host where the owner rule does not exist: both null
	u := goodUsage()
	u.OwnerAtStart, u.OwnerSeen = nil, nil
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestJobUsageRefusesWhatCannotBeRight(t *testing.T) {
	yes := true
	nan := math.NaN()
	for name, mut := range map[string]func(*JobUsage){
		"no job id": func(u *JobUsage) { u.JobID = "" },
		"no host":   func(u *JobUsage) { u.Host = "" },
		"no repo":   func(u *JobUsage) { u.Repo = "" },
		"no task":   func(u *JobUsage) { u.Task = "" },
		"owner there at the start but never seen": func(u *JobUsage) {
			yes, no := true, false
			u.OwnerAtStart, u.OwnerSeen = &yes, &no
		},
		"an unknown slot":             func(u *JobUsage) { u.Slot = "medium" },
		"an unknown class":            func(u *JobUsage) { u.Class = "urgent" },
		"an unknown source":           func(u *JobUsage) { u.Source = "cron" },
		"a job still queued":          func(u *JobUsage) { u.Result = StateQueued },
		"a job still running":         func(u *JobUsage) { u.Result = StateRunning },
		"no start":                    func(u *JobUsage) { u.StartedAt = time.Time{} },
		"ended before it began":       func(u *JobUsage) { u.EndedAt = u.StartedAt.Add(-time.Second) },
		"negative duration":           func(u *JobUsage) { u.DurationS = -1 },
		"negative cpu":                func(u *JobUsage) { u.CPUSeconds = -1 },
		"nan cpu":                     func(u *JobUsage) { u.CPUSeconds = nan },
		"infinite cores":              func(u *JobUsage) { u.PeakCores = math.Inf(1) },
		"negative average":            func(u *JobUsage) { u.AvgCores = -0.5 },
		"negative memory":             func(u *JobUsage) { u.MemPeakMB = -1 },
		"an unknown cpu source":       func(u *JobUsage) { u.CPUSource = "guess" },
		"an unknown cores measure":    func(u *JobUsage) { u.PeakCoresMeasure = "rusage" },
		"an unknown memory measure":   func(u *JobUsage) { u.MemPeakMeasure = "" },
		"a negative job count":        func(u *JobUsage) { u.JobsAtStart = -1 },
		"a peak under the start":      func(u *JobUsage) { u.JobsPeak = 0 },
		"an unknown zone":             func(u *JobUsage) { u.ZoneAtStart = "calm" },
		"an unknown worst zone":       func(u *JobUsage) { u.ZoneWorst = "" },
		"owner at start without seen": func(u *JobUsage) { u.OwnerSeen = nil },
		"owner seen without start":    func(u *JobUsage) { u.OwnerAtStart, u.OwnerSeen = nil, &yes },
	} {
		u := goodUsage()
		mut(&u)
		if u.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestJobUsageRoundTripsAndDecodesStrictly(t *testing.T) {
	b, err := json.Marshal(goodUsage())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode[JobUsage](b)
	if err != nil || got.JobID != "j_01JC" || got.CPUSource != CPUFromRusage || got.OwnerSeen == nil || !*got.OwnerSeen {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Decode[JobUsage]([]byte(`{"job_id":"x","surprise":1}`)); err == nil {
		t.Fatal("an unknown field is refused")
	}
	// the owner fields are present as null where they do not apply, not omitted
	u := goodUsage()
	u.OwnerAtStart, u.OwnerSeen = nil, nil
	b, _ = json.Marshal(u)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(b, &raw)
	if string(raw["owner_at_start"]) != "null" || string(raw["owner_seen"]) != "null" {
		t.Fatalf("%s %s", raw["owner_at_start"], raw["owner_seen"])
	}
}

func TestUsageResponseValidates(t *testing.T) {
	r := UsageResponse{Host: "air26", Since: usageT0, Records: []JobUsage{goodUsage()}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if (UsageResponse{Since: usageT0}).Validate() == nil {
		t.Fatal("a response names its host")
	}
	r.Records[0].Host = ""
	if r.Validate() == nil {
		t.Fatal("a bad record is refused")
	}
	// a next_since only goes with truncated
	r = UsageResponse{Host: "air26", Since: usageT0, Records: []JobUsage{goodUsage()}, Truncated: true}
	if r.Validate() == nil {
		t.Fatal("truncated names where the next page starts")
	}
	next := usageT0
	r.NextSince = &next
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Truncated = false
	if r.Validate() == nil {
		t.Fatal("a whole answer has no next page")
	}
}
