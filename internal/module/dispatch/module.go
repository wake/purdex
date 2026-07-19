package dispatch

import (
	"context"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/wake/purdex/internal/core"
)

// Environment keys for the Ploom connection. These are placeholder sourcing for
// M0 P.3 — the token/URL are read here but never hardcoded; a first-class config
// surface can replace this later without touching the client/worker.
const (
	envPloomURL   = "PDX_PLOOM_URL"
	envPloomToken = "PDX_PLOOM_TOKEN"
)

// DispatchModule wires the Ploom consumer: it polls pending dispatches, claims
// them, and fetches their full detail (M0 Task P.3). It stops at fetch — the
// FetchSink is the seam where P.7 attaches execution create → admission → launch
// → report. Depending on the execution module keeps that wiring ordering ready.
type DispatchModule struct {
	core   *core.Core
	client *Client
	worker *Worker
	sink   FetchSink

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns a new DispatchModule ready for registration.
func New() *DispatchModule { return &DispatchModule{} }

func (m *DispatchModule) Name() string { return "dispatch" }

// Dependencies declares execution so this module initialises after the execution
// runtime store is ready — P.7 will pull execution.Store() into the FetchSink.
func (m *DispatchModule) Dependencies() []string { return []string{"execution"} }

// Init reads the Ploom connection (URL + Bearer token) from the environment and
// builds the client + worker. When no URL is configured the consumer stays
// disabled so the daemon still boots (M0 has no default Ploom endpoint yet).
func (m *DispatchModule) Init(c *core.Core) error {
	m.core = c

	baseURL := os.Getenv(envPloomURL)
	token := os.Getenv(envPloomToken)
	if baseURL == "" {
		return nil // consumer disabled — see Start
	}

	m.client = NewClient(baseURL, token)
	// P.3 sink: log only. P.7 replaces this with the real consume-loop tail.
	sink := m.sink
	if sink == nil {
		sink = func(_ context.Context, claimed ClaimedDispatch) {
			log.Printf("[dispatch] claimed+fetched dispatch=%s issue=%s repo=%s (P.7 sink pending)",
				claimed.Pending.DispatchID, claimed.Detail.Issue.IssueID, claimed.Detail.RepoLocation.LocalDir)
		}
	}
	m.worker = NewWorker(m.client, WithSink(sink))
	return nil
}

// RegisterRoutes reserves no HTTP surface; the dispatch consumer is pull-only.
func (m *DispatchModule) RegisterRoutes(_ *http.ServeMux) {}

// Start launches the poll loop in the background when a Ploom endpoint is
// configured; otherwise it logs that the consumer is disabled.
func (m *DispatchModule) Start(_ context.Context) error {
	if m.worker == nil {
		log.Printf("[dispatch] consumer disabled (%s not set)", envPloomURL)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.worker.Run(ctx)
	}()
	log.Println("[dispatch] consumer started")
	return nil
}

// Stop cancels the poll loop and waits for it to drain.
func (m *DispatchModule) Stop(_ context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}
	return nil
}
