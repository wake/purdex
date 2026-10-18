package wire

// Zone is the derived pressure judgement (spec §3.2).
type Zone string

const (
	ZoneNormal  Zone = "normal"
	ZoneWarning Zone = "warning"
	ZoneDanger  Zone = "danger"
	ZoneUnknown Zone = "unknown"
)

func (z Zone) Valid() bool {
	switch z {
	case ZoneNormal, ZoneWarning, ZoneDanger, ZoneUnknown:
		return true
	}
	return false
}

// OwnerKind attributes a process (spec §3.1).
type OwnerKind string

const (
	OwnerJob      OwnerKind = "job"
	OwnerSession  OwnerKind = "session"
	OwnerCodex    OwnerKind = "codex"
	OwnerResident OwnerKind = "resident"
	OwnerSystem   OwnerKind = "system"
)

func (k OwnerKind) Valid() bool {
	switch k {
	case OwnerJob, OwnerSession, OwnerCodex, OwnerResident, OwnerSystem:
		return true
	}
	return false
}

// JobClass is the job priority class: release > merge > dev (spec §4.1).
type JobClass string

const (
	ClassRelease JobClass = "release"
	ClassMerge   JobClass = "merge"
	ClassDev     JobClass = "dev"
)

func (c JobClass) Valid() bool {
	switch c {
	case ClassRelease, ClassMerge, ClassDev:
		return true
	}
	return false
}

// Position is a queued job's manual place (spec §4.6, "Manual order"). On a job the empty string is no manual place (the worker's score
// decides); the four names are what a request may ask for, and "normal" in a request clears the place.
type Position string

const (
	PositionNormal Position = "normal" // request only: no manual place
	PositionFront  Position = "front"  // before every scored job, the latest asked first
	PositionBack   Position = "back"   // behind every scored job, the latest asked last
	PositionHold   Position = "hold"   // not handed to any worker until let go
)

// ValidRequest says p is a position a request may ask for.
func (p Position) ValidRequest() bool {
	switch p {
	case PositionNormal, PositionFront, PositionBack, PositionHold:
		return true
	}
	return false
}

// ValidOnJob says p is a position a job may carry: none, front, back or hold.
func (p Position) ValidOnJob() bool {
	return p == "" || (p != PositionNormal && p.ValidRequest())
}

// JobState is a job's state (spec §4.2).
type JobState string

const (
	StateQueued     JobState = "queued"
	StateClaimed    JobState = "claimed"
	StateRunning    JobState = "running"
	StateSucceeded  JobState = "succeeded"
	StateFailed     JobState = "failed"
	StateCancelled  JobState = "cancelled"
	StateSuperseded JobState = "superseded"
	StateExpired    JobState = "expired"
)

func (s JobState) Valid() bool {
	switch s {
	case StateQueued, StateClaimed, StateRunning, StateSucceeded, StateFailed,
		StateCancelled, StateSuperseded, StateExpired:
		return true
	}
	return false
}

// Terminal reports whether no further transition happens.
func (s JobState) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled, StateSuperseded, StateExpired:
		return true
	}
	return false
}

// ResultState reports whether a worker may post s through `result`.
func (s JobState) ResultState() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled, StateSuperseded:
		return true
	}
	return false
}

// ReleaseReason is why a worker hands a claim back (spec §4.3).
type ReleaseReason string

const (
	ReleaseReclaim ReleaseReason = "reclaim"
	ReleaseInfra   ReleaseReason = "infra"
)

func (r ReleaseReason) Valid() bool { return r == ReleaseReclaim || r == ReleaseInfra }

// EventType is the SSE event envelope type (spec §3.3).
type EventType string

const (
	EventFacts     EventType = "facts"
	EventJob       EventType = "job"
	EventLabels    EventType = "labels"
	EventException EventType = "exception"
	EventWorker    EventType = "worker"
)

func (t EventType) Valid() bool {
	switch t {
	case EventFacts, EventJob, EventLabels, EventException, EventWorker:
		return true
	}
	return false
}

// Hold names a slot class an exception holds (spec §9).
type Hold string

const (
	HoldHeavy Hold = "heavy"
	HoldLight Hold = "light"
)

func (h Hold) Valid() bool { return h == HoldHeavy || h == HoldLight }
