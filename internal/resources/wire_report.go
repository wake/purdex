package resources

// The monitoring report (spec §6, R10): what the stored host minutes say about whether the host's load control suits it. It reads
// stored rows only and never samples. Since L2c it reads host_minutes alone: the lease tables are no part of it.

// MaxReportSince is the longest period a report may ask for: the retention of
// the rows it reads.
const MaxReportSince = 14 * 24 // hours

// ReportPath is the route.
const ReportPath = "/api/resources/report"

// Report is the answer of GET /api/resources/report. Every figure is worked out from the minute rows of the period, so the same
// rows always give the same bytes.
type Report struct {
	// Since and Until are the period, unix ms.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Coverage says which part of the period the host timeline has rows for: a
	// report over a period the timeline only partly covers shows it.
	Coverage ReportCoverage `json:"coverage"`
	Full     ReportFull     `json:"full"`
	// Heavy is what the host did while the gate had a heavy command running.
	Heavy ReportHeavy `json:"heavy"`
	// Light is the light commands the gate let run.
	Light ReportLight `json:"light"`
	// Gate is how often commands waited for a slot.
	Gate ReportGate `json:"gate"`
	// Zones is the share of time per zone.
	Zones ReportZones `json:"zones"`
}

// ReportZones is the share of sampled time in each zone, from the minute rows
// that carry sampled_s. Minutes is how many rows counted: a row written before
// the columns existed has no zone seconds and is left out. All zero with none.
type ReportZones struct {
	Minutes  int     `json:"minutes"`
	SampledS int     `json:"sampled_s"`
	Normal   float64 `json:"normal"`
	Warning  float64 `json:"warning"`
	Danger   float64 `json:"danger"`
}

// ReportCoverage is the host timeline's reach in the period.
type ReportCoverage struct {
	// From and To are the first and the last minute row (unix ms of the
	// minute's start); both 0 with no rows. Minutes is how many rows there are.
	From    int64 `json:"from"`
	To      int64 `json:"to"`
	Minutes int   `json:"minutes"`
}

// ReportFull is how long the host was full, from the minute rows.
type ReportFull struct {
	// Share is Σ full_ticks × the sample interval over the covered minutes (0
	// to 1; 0 with no coverage).
	Share float64 `json:"share"`
	Runs  int     `json:"runs"`
	// LongestS is the longest run, seconds.
	LongestS int `json:"longest_s"`
}

// ReportHeavy is what the host did while the gate had a heavy command running. Minutes counts the minutes with at least one (the
// gate lets one run), MaxRunning is the largest number in a minute; both come from the gate's heavy_running alone. LegacyMinutes is
// the retired lease's figure for the rows written before L2 (minutes with two or more heavy leases held): a different thing, kept
// apart so that the two are never added up.
type ReportHeavy struct {
	Minutes       int     `json:"minutes"`
	MaxRunning    int     `json:"max_running"`
	MaxLoad1      float64 `json:"max_load1"`
	MaxMem        float64 `json:"max_mem"`
	LegacyMinutes int     `json:"legacy_minutes"`
}

// ReportLight is the gate's light slots in use.
type ReportLight struct {
	// MaxRunning is the largest number of light commands running in any minute.
	MaxRunning int `json:"max_running"`
}

// ReportGate is the waiting at the gate, from the minute peaks of gate_waiting.
type ReportGate struct {
	// WaitingMinutes is how many minutes had a command waiting at the gate at some moment; MaxWaiting the longest queue seen.
	WaitingMinutes int `json:"waiting_minutes"`
	MaxWaiting     int `json:"max_waiting"`
}
