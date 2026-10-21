package wire

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// Info is GET /v1/info (spec §3.3).
type Info struct {
	HostID   string     `json:"host_id"`
	Alias    string     `json:"alias"`
	Version  string     `json:"version"`
	Proto    int        `json:"proto"`
	Platform string     `json:"platform"`
	Arch     string     `json:"arch"`
	Worker   InfoWorker `json:"worker"`
	Labels   []string   `json:"labels"`
	// Fingerprint is the OS version and every tool's full version (spec §7, §9);
	// empty before the first facts record.
	Fingerprint map[string]string `json:"fingerprint"`
	// Fault says which test hooks this host runs with (docs/wwd-c2-matrix.md section 7); absent, the normal case, when none is on.
	Fault *InfoFault `json:"fault,omitempty"`
}

// InfoFault is the hooks that are on. A reader decides from it whether a matrix case can run here.
type InfoFault struct {
	LogUploadFail   bool   `json:"log_upload_fail,omitempty"`
	OwnerActiveFile bool   `json:"owner_active_file,omitempty"`
	Zone            string `json:"zone,omitempty"`
	SnapshotDelayS  int    `json:"snapshot_delay_s,omitempty"`
}

type InfoWorker struct {
	Enabled bool `json:"enabled"`
	// ExplicitLabelsOnly says the worker takes only a job whose labels name an os: label, an earned task: label or a host: label (the host's
	// `[worker] explicit_labels_only`; mserv). Absent means false, which is also what a wwd from before the field says: a reader that asks
	// "can this host take the job?" then answers yes for a job it will not, never the other way round (S3-3b).
	ExplicitLabelsOnly bool `json:"explicit_labels_only,omitempty"`
	// SpeedFactor is this host's speed against the reference host (`[worker] speed_factor`, 1 when it is not set): the multiplier of a job's
	// duration here, so a larger number is a slower host. A wwd always announces a number above 0; absent (0) means a wwd from before the
	// field, whose speed is not known, and a reader then does not take it to be faster than anyone (#3904).
	SpeedFactor float64 `json:"speed_factor,omitempty"`
}

func (i Info) Validate() error {
	if i.Proto < MinProto || i.Proto > Proto {
		return fmt.Errorf("info: unsupported proto %d", i.Proto)
	}
	return nil
}

// Peer is one entry of GET /v1/pool: info, latest facts, reachability.
// Facts is null for a peer never reached.
type Peer struct {
	Info      Info   `json:"info"`
	Facts     *Facts `json:"facts"`
	Reachable bool   `json:"reachable"`
	// RttMS is how long the GET /v1/facts request of the last poll took, in ms (that request alone, not the /v1/info before it). Null for
	// this host itself, for a peer that did not answer, and when its facts did not come.
	RttMS *float64 `json:"rtt_ms,omitempty"`
	// Liveness says, for a peer whose wwd does not answer, whether its host does (a TCP connect to its SSH port, nothing read or sent):
	// wwd_down (the host answers), host_down (nothing does). ok when the wwd answers; unknown before the first look. Present only when
	// the reader asked for it (PoolLivenessQuery). LivenessAt is when it was last looked at (moves at every poll); LivenessSince is since
	// when the peer has had this liveness (moves only when the value does, and is set when it comes back to ok as well).
	Liveness      Liveness   `json:"liveness,omitempty"`
	LivenessAt    *time.Time `json:"liveness_at,omitempty"`
	LivenessSince *time.Time `json:"liveness_since,omitempty"`
}

// Liveness is what a look at a peer's SSH port, made when its wwd does not answer, found.
type Liveness string

const (
	LivenessOK       Liveness = "ok"        // the wwd answers
	LivenessWwdDown  Liveness = "wwd_down"  // the wwd does not, the host does
	LivenessHostDown Liveness = "host_down" // neither does: the host is off or the network is cut
	LivenessUnknown  Liveness = "unknown"   // not looked at yet
)

// Valid says whether l is one of the four (the empty one, absent on the wire, is checked by the caller).
func (l Liveness) Valid() bool {
	switch l {
	case LivenessOK, LivenessWwdDown, LivenessHostDown, LivenessUnknown:
		return true
	}
	return false
}

// PoolLivenessQuery is the query that asks GET /v1/pool for the liveness fields. The route is read by the Purdex daemon on the same host,
// which is not a wwd of the pool, so PoolAtLeast cannot say whether it can read them; a reader that knows the fields asks for them, and
// one that does not never sees a name it would reject. Another wwd, which ignores the query, simply does not answer with them.
const PoolLivenessQuery = "liveness=1"

// Pool is GET /v1/pool.
type Pool struct {
	Peers []Peer `json:"peers"`
}

func (p Pool) Validate() error {
	for i, peer := range p.Peers {
		if err := peer.Info.Validate(); err != nil {
			return fmt.Errorf("pool: peers[%d]: %w", i, err)
		}
		if peer.Liveness != "" && !peer.Liveness.Valid() {
			return fmt.Errorf("pool: peers[%d]: unknown liveness %q", i, peer.Liveness)
		}
		if peer.Facts != nil {
			if err := peer.Facts.Validate(); err != nil {
				return fmt.Errorf("pool: peers[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// History steps.
const (
	Step1m = "1m"
	Step5s = "5s"
)

// FactsHistoryQuery is GET /v1/facts/history?since=&step=. Host, when set, is the alias of another host of the pool: on the socket the
// local wwd forwards the question to it (spec §10); it is not part of what ParseFactsHistoryQuery reads (the route takes it apart).
type FactsHistoryQuery struct {
	Since time.Time
	Step  string
	Host  string
}

// ParseFactsHistoryQuery parses the query string; step defaults to 5s.
func ParseFactsHistoryQuery(v url.Values) (FactsHistoryQuery, error) {
	var q FactsHistoryQuery
	t, err := time.Parse(time.RFC3339, v.Get("since"))
	if err != nil {
		return q, fmt.Errorf("history: since: %w", err)
	}
	q.Since = t
	q.Step = v.Get("step")
	if q.Step == "" {
		q.Step = Step5s
	}
	if q.Step != Step1m && q.Step != Step5s {
		return q, fmt.Errorf("history: invalid step %q", q.Step)
	}
	return q, nil
}

func (q FactsHistoryQuery) Values() url.Values {
	v := url.Values{"since": {q.Since.Format(time.RFC3339)}, "step": {q.Step}}
	if q.Host != "" {
		v.Set("host", q.Host)
	}
	return v
}

type FactsHistoryResponse struct {
	Step    string  `json:"step"`
	Records []Facts `json:"records"`
}

func (r FactsHistoryResponse) Validate() error {
	if r.Step != Step1m && r.Step != Step5s {
		return fmt.Errorf("history: invalid step %q", r.Step)
	}
	for i, f := range r.Records {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("history: records[%d]: %w", i, err)
		}
	}
	return nil
}

// ---- events (SSE envelope, spec §3.3) ----

// Event is the SSE envelope; Data is decoded per Type by DecodeData.
type Event struct {
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

type JobEvent struct {
	Host string `json:"host"`
	Job  Job    `json:"job"`
}

func (j JobEvent) Validate() error { return j.Job.Validate() }

type LabelsEvent struct {
	Host   string   `json:"host"`
	Labels []string `json:"labels"`
}

type ExceptionEvent struct {
	Host      string     `json:"host"`
	Name      string     `json:"name"`
	Active    bool       `json:"active"`
	Exception *Exception `json:"exception"`
}

func (e ExceptionEvent) Validate() error {
	if e.Exception != nil {
		return e.Exception.Validate()
	}
	return nil
}

type WorkerEvent struct {
	Host   string         `json:"host"`
	Worker WorkerSettings `json:"worker"`
}

func (e Event) Validate() error {
	if !e.Type.Valid() {
		return fmt.Errorf("event: invalid type %q", e.Type)
	}
	if len(e.Data) == 0 {
		return fmt.Errorf("event: missing data")
	}
	_, err := e.DecodeData()
	return err
}

// DecodeData decodes Data (leniently: an event may come from a later build, see DecodeLenient) into the type for e.Type (Facts, JobEvent,
// LabelsEvent, ExceptionEvent, WorkerEvent or AlertEvent, by value).
func (e Event) DecodeData() (any, error) {
	switch e.Type {
	case EventFacts:
		return DecodeLenient[Facts](e.Data)
	case EventJob:
		return DecodeLenient[JobEvent](e.Data)
	case EventLabels:
		return DecodeLenient[LabelsEvent](e.Data)
	case EventException:
		return DecodeLenient[ExceptionEvent](e.Data)
	case EventWorker:
		return DecodeLenient[WorkerEvent](e.Data)
	case EventAlert:
		return DecodeLenient[AlertEvent](e.Data)
	}
	return nil, fmt.Errorf("event: invalid type %q", e.Type)
}

// ---- worker settings and exceptions (spec §9) ----

type WorkerSettings struct {
	Enabled   bool   `json:"enabled"`
	Slots     Slots  `json:"slots"`
	OwnerRule string `json:"owner_rule"`
	// DiskMinGB is the free space the host's work volume must have for a heavy run to start (queue job or adhoc command); 0 is no
	// floor. In a PUT, absent keeps the current value (a client that does not know the field must not switch the floor off); wwd
	// always answers with it.
	DiskMinGB *float64 `json:"disk_min_gb,omitempty"`
}

// DefaultDiskMinGB is the disk floor of a host that has not set one: the pdx gate's [gate.admission] disk_min_gb default.
const DefaultDiskMinGB = 10.0

// Exception is POST /v1/exceptions: Until or PID bounds it (the other is null).
type Exception struct {
	Name  string     `json:"name"`
	Until *time.Time `json:"until"`
	PID   *int       `json:"pid"`
	Holds []Hold     `json:"holds"`
	// Allow and Flags make an exception a hold (spec §13.4). Absent, the exception means what it always meant. Present,
	// the worker admits a job only if its class or its repo is listed (both lists empty: none).
	Allow *HoldAllow `json:"allow,omitempty"`
	Flags []HoldFlag `json:"flags,omitempty"`
}

func (e Exception) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("exception: missing name")
	}
	if (e.Until == nil) == (e.PID == nil) {
		return fmt.Errorf("exception: exactly one of until and pid is required")
	}
	if e.PID != nil && *e.PID <= 0 {
		return fmt.Errorf("exception: invalid pid %d", *e.PID)
	}
	for _, h := range e.Holds {
		if !h.Valid() {
			return fmt.Errorf("exception: invalid hold %q", h)
		}
	}
	if err := validateHoldFields(e.Allow, e.Flags); err != nil {
		return fmt.Errorf("exception: %w", err)
	}
	return nil
}

// ---- Purdex daemon API for the App (spec §10) ----

// HeadroomReasons says why a side of a host's headroom is 0: the codes of spec §3.2 in this order, worker_off, zone_warning |
// zone_danger | zone_unknown, exception:<name>, owner_active, slots_full (gate_full is no longer produced, the string stays for older readers; and unknown for a side at 0 that a faulty record does not explain). An empty list means there is room. Both lists are
// never null.
type HeadroomReasons struct {
	Heavy []string `json:"heavy"`
	Light []string `json:"light"`
}

// Load drivers: the reading that holds a host's composite load up (internal/fleet.LoadScore).
const (
	LoadDriverCPU    = "cpu"
	LoadDriverMem    = "mem"
	LoadDriverLevel  = "pressure"
	LoadDriverPaging = "paging"
	LoadDriverDisk   = "disk"
)

// HostLoad is a host's composite load, 0-100, and the reading that is the largest of those it is made of.
type HostLoad struct {
	Score  float64 `json:"score"`
	Driver string  `json:"driver"`
}

// PoolHost is one host in GET /api/pool/hosts. HeadroomReasons and Load come from the latest facts and are null for a host that has
// none (capability pool.assess.v1).
type PoolHost struct {
	Info            Info             `json:"info"`
	Facts           *Facts           `json:"facts"`
	Worker          WorkerSettings   `json:"worker"`
	Labels          []string         `json:"labels"`
	Reachable       bool             `json:"reachable"`
	HeadroomReasons *HeadroomReasons `json:"headroom_reasons"`
	Load            *HostLoad        `json:"load"`
	// RttMS is Peer's, passed on to the App (null: the local host, a peer that did not answer, or an older wwd).
	RttMS *float64 `json:"rtt_ms,omitempty"`
	// Liveness, LivenessAt and LivenessSince are Peer's, passed on to the App; absent while the pool does not carry them.
	Liveness      Liveness   `json:"liveness,omitempty"`
	LivenessAt    *time.Time `json:"liveness_at,omitempty"`
	LivenessSince *time.Time `json:"liveness_since,omitempty"`
}

// PoolHostExceptions is GET /api/pool/hosts/{alias}/exceptions: the exceptions in force on a host with their holds (facts' exceptions[]
// has none). Known is false when the host's wwd predates the route (it answered 404): nothing is claimed about it, and Exceptions is empty.
type PoolHostExceptions struct {
	Host       string      `json:"host"`
	Known      bool        `json:"known"`
	Exceptions []Exception `json:"exceptions"`
}

type PoolHostsResponse struct {
	Hosts []PoolHost `json:"hosts"`
}

func (r PoolHostsResponse) Validate() error {
	for i, h := range r.Hosts {
		if err := h.Info.Validate(); err != nil {
			return fmt.Errorf("hosts[%d]: %w", i, err)
		}
		if h.Liveness != "" && !h.Liveness.Valid() {
			return fmt.Errorf("hosts[%d]: unknown liveness %q", i, h.Liveness)
		}
		if h.Facts != nil {
			if err := h.Facts.Validate(); err != nil {
				return fmt.Errorf("hosts[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// JobsListResponse is the answer to GET /v1/jobs. Cursor is the origin's position after the last change the answer carries: sent back as
// ?after=<cursor> it returns the jobs that changed since, each once, in the state it is in now. It is a decimal string made on the
// origin (never a time of a worker's) that the client treats as opaque. It is empty only in an answer that is not a cursor read.
//
// Epoch names the database the cursor belongs to: it is made when the queue's tables are created and never changes after, so a wwd.db
// that was removed and made again has another one. A client keeps it with its cursor, and a cursor kept under a different epoch means
// nothing here: it starts again from "0". It is empty only from a server that predates it.
type JobsListResponse struct {
	Jobs   []Job  `json:"jobs"`
	Cursor string `json:"cursor"`
	Epoch  string `json:"epoch"`
}

var cursorRe = regexp.MustCompile(`^[0-9]{1,18}$`)

// ValidCursor reports whether s has the form of a cursor (what ?after= takes and JobsListResponse.Cursor carries).
func ValidCursor(s string) bool { return cursorRe.MatchString(s) }

var epochRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidEpoch reports whether s has the form of a database epoch.
func ValidEpoch(s string) bool { return epochRe.MatchString(s) }

// CursorAheadReason is the only reason a CursorAhead carries.
const CursorAheadReason = "cursor_ahead"

// CursorAhead is the 409 body of GET /v1/jobs?after=<cursor> when the cursor is past the origin's last change: it was made by another
// database (this one was removed and made again), so reading from it would find nothing, forever. Head and Epoch are this database's;
// the client starts again from "0" under the new epoch.
type CursorAhead struct {
	Reason string `json:"reason"`
	Head   string `json:"head"`
	Epoch  string `json:"epoch"`
}

func (c CursorAhead) Validate() error {
	switch {
	case c.Reason != CursorAheadReason:
		return fmt.Errorf("cursor ahead: unexpected reason %q", c.Reason)
	case !ValidCursor(c.Head):
		return fmt.Errorf("cursor ahead: invalid head %q", c.Head)
	case !ValidEpoch(c.Epoch):
		return fmt.Errorf("cursor ahead: invalid epoch %q", c.Epoch)
	}
	return nil
}

func (r JobsListResponse) Validate() error {
	if r.Cursor != "" && !ValidCursor(r.Cursor) {
		return fmt.Errorf("jobs list: invalid cursor %q", r.Cursor)
	}
	if r.Epoch != "" && !ValidEpoch(r.Epoch) {
		return fmt.Errorf("jobs list: invalid epoch %q", r.Epoch)
	}
	for i, j := range r.Jobs {
		if err := j.Validate(); err != nil {
			return fmt.Errorf("jobs[%d]: %w", i, err)
		}
	}
	return nil
}

// JobSubmissionResponse is POST /v1/jobs (local socket only): {id}, or
// {id, result} at once on a cache hit; Result is omitted otherwise (spec §4.4).
type JobSubmissionResponse struct {
	ID string `json:"id"`
	// omitempty is the §4.4 exception to the "absent is null, never omitted" rule.
	Result *Result `json:"result,omitempty"`
}

func (r JobSubmissionResponse) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("submission: missing id")
	}
	return nil
}

// JobDetail is GET /api/pool/jobs/{id}; Log is null until the log is stored.
type JobDetail struct {
	Job Job     `json:"job"`
	Log *string `json:"log"`
}

func (d JobDetail) Validate() error { return d.Job.Validate() }

// PoolChanged is the pool.changed host event payload.
type PoolChanged struct {
	Host string `json:"host"`
}

// JobsChanged is the jobs.changed host event payload.
type JobsChanged struct {
	Host  string   `json:"host"`
	JobID string   `json:"job_id"`
	State JobState `json:"state"`
}

func (c JobsChanged) Validate() error {
	if !c.State.Valid() {
		return fmt.Errorf("jobs.changed: invalid state %q", c.State)
	}
	return nil
}
