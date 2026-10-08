package resources

// The monitoring report (spec D-8.3, R10): what the stored lease rows and the
// host timeline say about whether the admission rule suits the host. It reads
// stored rows only and never samples.

// MaxReportSince is the longest period a report may ask for: the retention of
// the rows it reads.
const MaxReportSince = 14 * 24 // hours

// ReportPath is the route.
const ReportPath = "/api/resources/report"

// Report is the answer of GET /api/resources/report. Every list is sorted, so
// the same rows always give the same bytes.
type Report struct {
	// Since and Until are the period, unix ms.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Coverage says which part of the period the host timeline has rows for: a
	// report over a period the timeline only partly covers shows it.
	Coverage ReportCoverage `json:"coverage"`
	// NotRecorded counts leases of the period that were granted before the
	// decision record existed (dec_recorded = 0): they are in no figure that
	// needs the decision.
	NotRecorded int `json:"not_recorded"`

	Requests ReportRequests `json:"requests"`
	// Paths counts the recorded grants by how they were let in.
	Paths ReportPaths `json:"paths"`
	// WaitMS is the wait of the recorded grants, nearest-rank percentiles.
	WaitMS ReportWait `json:"wait_ms"`
	// WouldWaitR2 is how many recorded grants the pre-R9 formula would have
	// held back at the moment they were granted.
	WouldWaitR2 int         `json:"would_wait_r2"`
	Full        ReportFull  `json:"full"`
	Heavy       ReportHeavy `json:"heavy"`
	// Kinds is the measured use of each kind against its weight.
	Kinds []ReportKind `json:"kinds"`
}

// ReportCoverage is the host timeline's reach in the period.
type ReportCoverage struct {
	// From and To are the first and the last minute row (unix ms of the
	// minute's start); both 0 with no rows. Minutes is how many rows there are.
	From    int64 `json:"from"`
	To      int64 `json:"to"`
	Minutes int   `json:"minutes"`
}

// ReportRequests counts the leases created in the period.
type ReportRequests struct {
	Total int `json:"total"`
	// NotGranted are those that never got in (cancelled, abandoned, or still
	// waiting).
	NotGranted int               `json:"not_granted"`
	ByKind     []ReportKindCount `json:"by_kind"`
}

// ReportKindCount is one kind's number of requests. Kind is "" for a request
// that named a weight.
type ReportKindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// ReportPaths counts recorded grants by path (D-8.1).
type ReportPaths struct {
	Immediate int `json:"immediate"`
	Waited    int `json:"waited"`
	Overrun   int `json:"overrun"`
}

// ReportWait is the wait percentiles in ms; all 0 with no recorded grant.
type ReportWait struct {
	P50 int64 `json:"p50"`
	P90 int64 `json:"p90"`
	Max int64 `json:"max"`
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

// ReportHeavy is what the host did while two or more heavy leases were held.
type ReportHeavy struct {
	Minutes  int     `json:"minutes"`
	MaxLoad1 float64 `json:"max_load1"`
	MaxMem   float64 `json:"max_mem"`
}

// ReportKind is one kind's measured use. Weight is the largest weight the kind
// was granted with in the period. PeakMax is the largest peak of any of its
// leases and MeanAvg the average of their means, both host percent, over the
// granted leases that were measured.
type ReportKind struct {
	Kind    string  `json:"kind"`
	N       int     `json:"n"`
	Weight  int     `json:"weight"`
	PeakMax float64 `json:"peak_max"`
	MeanAvg float64 `json:"mean_avg"`
}
