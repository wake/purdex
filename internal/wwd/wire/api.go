package wire

import (
	"encoding/json"
	"fmt"
	"net/url"
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
}

type InfoWorker struct {
	Enabled bool `json:"enabled"`
}

func (i Info) Validate() error {
	if i.Proto != Proto {
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
}

// Pool is GET /v1/pool.
type Pool struct {
	Peers []Peer `json:"peers"`
}

// History steps.
const (
	Step1m = "1m"
	Step5s = "5s"
)

// FactsHistoryQuery is GET /v1/facts/history?since=&step=.
type FactsHistoryQuery struct {
	Since time.Time
	Step  string
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
	return url.Values{"since": {q.Since.Format(time.RFC3339)}, "step": {q.Step}}
}

type FactsHistoryResponse struct {
	Step    string  `json:"step"`
	Records []Facts `json:"records"`
}

func (r FactsHistoryResponse) Validate() error {
	if r.Step != Step1m && r.Step != Step5s {
		return fmt.Errorf("history: invalid step %q", r.Step)
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

// DecodeData strictly decodes Data into the type for e.Type (Facts, JobEvent,
// LabelsEvent, ExceptionEvent or WorkerEvent, by value).
func (e Event) DecodeData() (any, error) {
	switch e.Type {
	case EventFacts:
		return Decode[Facts](e.Data)
	case EventJob:
		return Decode[JobEvent](e.Data)
	case EventLabels:
		return Decode[LabelsEvent](e.Data)
	case EventException:
		return Decode[ExceptionEvent](e.Data)
	case EventWorker:
		return Decode[WorkerEvent](e.Data)
	}
	return nil, fmt.Errorf("event: invalid type %q", e.Type)
}

// ---- worker settings and exceptions (spec §9) ----

type WorkerSettings struct {
	Enabled   bool   `json:"enabled"`
	Slots     Slots  `json:"slots"`
	OwnerRule string `json:"owner_rule"`
}

// Exception is POST /v1/exceptions: Until or PID bounds it (the other is null).
type Exception struct {
	Name  string     `json:"name"`
	Until *time.Time `json:"until"`
	PID   *int       `json:"pid"`
	Holds []Hold     `json:"holds"`
}

func (e Exception) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("exception: missing name")
	}
	for _, h := range e.Holds {
		if !h.Valid() {
			return fmt.Errorf("exception: invalid hold %q", h)
		}
	}
	return nil
}

// ---- Purdex daemon API for the App (spec §10) ----

// PoolHost is one host in GET /api/pool/hosts.
type PoolHost struct {
	Info      Info           `json:"info"`
	Facts     *Facts         `json:"facts"`
	Worker    WorkerSettings `json:"worker"`
	Labels    []string       `json:"labels"`
	Reachable bool           `json:"reachable"`
}

type PoolHostsResponse struct {
	Hosts []PoolHost `json:"hosts"`
}

type JobsListResponse struct {
	Jobs []Job `json:"jobs"`
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
