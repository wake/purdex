package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/module/stream"
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
	outbox *Outbox
	sender *Sender

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns a new DispatchModule ready for registration.
func New() *DispatchModule { return &DispatchModule{} }

func (m *DispatchModule) Name() string { return "dispatch" }

// Dependencies declares execution AND stream: execution publishes the runtime
// store (durability cut + launch fence), and stream publishes the relay gateway
// the P.7 launcher probes/pushes through. Topological Init ordering therefore
// guarantees both registry keys exist before this module's Init reads them.
func (m *DispatchModule) Dependencies() []string { return []string{"execution", "stream"} }

// Init reads the Ploom connection (URL + Bearer token) from the environment and
// builds the client, report sender, and consume→launch sink. When no URL is
// configured the consumer stays disabled so the daemon still boots (M0 has no
// default Ploom endpoint yet).
func (m *DispatchModule) Init(c *core.Core) error {
	m.core = c

	baseURL := os.Getenv(envPloomURL)
	token := os.Getenv(envPloomToken)
	if baseURL == "" {
		return nil // consumer disabled — see Start
	}
	m.client = NewClient(baseURL, token)

	// P.4: durable report outbox + sender — built BEFORE the sink because the
	// P.7 launch reporter enqueues accepted/running through this sender. The
	// sender drains queued reports to Ploom with accepted-before-lifecycle
	// ordering, an ack cursor, and retry/backoff; the execution store is the
	// durability-cut source for reconstructing a lost accepted from its row.
	outbox, err := OpenOutbox(filepath.Join(c.Cfg.DataDir, "outbox.db"))
	if err != nil {
		return err
	}
	m.outbox = outbox
	var opts []SenderOption
	if reader := executionReader(c); reader != nil {
		opts = append(opts, WithExecutionReader(reader))
	}
	m.sender = NewSender(m.outbox, m.client, opts...)

	// P.7: assemble the launch durable-cut sink. A test may pre-inject m.sink;
	// otherwise build the real consume→launch sink when the execution store and
	// relay gateway are both available, degrading to a log-only sink if not (the
	// poll/claim/fetch pipeline still runs — nothing launches).
	if m.sink == nil {
		if coord := m.buildCoordinator(c); coord != nil {
			m.sink = m.consumeSink(coord)
		} else {
			m.sink = disabledLaunchSink
		}
	}
	m.worker = NewWorker(m.client, WithSink(m.sink))
	return nil
}

// buildCoordinator assembles the execution launch durable-cut Coordinator (P.6)
// from the registry-published dependencies: the execution store (row store +
// launch fence), the stream relay gateway (probe/push relay), core tmux, and the
// live daemon connection config. Returns nil (launch disabled) if the store or
// gateway is unavailable.
func (m *DispatchModule) buildCoordinator(c *core.Core) *execution.Coordinator {
	store := executionStore(c)
	if store == nil {
		log.Printf("[dispatch] launch disabled: execution store unavailable")
		return nil
	}
	gw := relayGateway(c)
	if gw == nil {
		log.Printf("[dispatch] launch disabled: stream relay gateway unavailable")
		return nil
	}
	launcher := execution.NewRealLauncher(c.Tmux, gw, session.EncodeSessionID, func() execution.LaunchConfig {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		return execution.LaunchConfig{Token: c.Cfg.Token, Port: c.Cfg.Port, Bind: c.Cfg.Bind}
	})
	// M0: no root containment (allowedRoots nil). A first-class allowlist config
	// can restrict admissible repos later without touching this seam.
	admitter := execution.NewAdmitter(store, nil)
	reporter := launchReporter{sender: m.sender}
	return execution.NewCoordinator(admitter, store, reporter, launcher)
}

// consumeSink is the P.7 tail of the consume loop: for each claimed+fetched
// dispatch it runs the launch durable cut (admission → row → accepted → launch →
// running). Admission rejections (no row ever created) are reported as failed
// here — the only actor that can tell Ploom, since reconcile (P.9) has no row to
// find. Launch/store failures leave the Coordinator-marked-failed row for the
// terminal outcome path (P.8) and reconcile (P.9) backstop.
func (m *DispatchModule) consumeSink(coord *execution.Coordinator) FetchSink {
	return func(ctx context.Context, cd ClaimedDispatch) {
		req := execution.LaunchRequest{
			DispatchID:     cd.Pending.DispatchID,
			RepoLocation:   cd.Detail.RepoLocation.LocalDir,
			Prompt:         buildPrompt(cd.Detail.Issue),
			SandboxProfile: cd.Detail.SandboxProfile,
		}
		if _, err := coord.Accept(ctx, req); err != nil {
			switch {
			case errors.Is(err, execution.ErrRepoBusy), errors.Is(err, execution.ErrCanonical):
				log.Printf("[dispatch] admission rejected dispatch=%s: %v", req.DispatchID, err)
				if rerr := reportAdmissionRejected(m.sender, newRejectionID(), req.DispatchID, cd.Detail, err); rerr != nil {
					log.Printf("[dispatch] report rejection dispatch=%s: %v", req.DispatchID, rerr)
				}
			default:
				// Row (if created) is already marked failed by the Coordinator with
				// accepted(1) durably enqueued; terminal failed is P.8's outcome path.
				log.Printf("[dispatch] launch failed dispatch=%s: %v", req.DispatchID, err)
			}
		}
	}
}

// disabledLaunchSink drops a claimed dispatch when launch is unavailable (no
// execution store / relay gateway). The dispatch was already claimed on Ploom;
// M0 leaves it for Ploom-side expiry (there is no row to reconcile).
func disabledLaunchSink(_ context.Context, cd ClaimedDispatch) {
	log.Printf("[dispatch] launch disabled — dropping claimed dispatch=%s issue=%s repo=%s",
		cd.Pending.DispatchID, cd.Detail.Issue.IssueID, cd.Detail.RepoLocation.LocalDir)
}

// newRejectionID mints a synthetic execution_id for an admission-rejection failed
// projection. No execution row is created for a rejection (admission never
// captured head/dirty and single-live must not admit it), so the id only keys
// the accepted(1)+failed(2) report pair in the outbox. The exc_rej_ prefix keeps
// it distinct from store-generated ids.
func newRejectionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "exc_rej_" + hex.EncodeToString(b[:])
}

// executionStore resolves the registry-published execution store, or nil when it
// is unavailable (execution module not registered).
func executionStore(c *core.Core) *execution.ExecutionStore {
	svc, ok := c.Registry.Get(execution.RegistryKey)
	if !ok {
		return nil
	}
	store, _ := svc.(*execution.ExecutionStore)
	return store
}

// relayGateway resolves the stream-published relay gateway as the narrow port the
// launcher needs, or nil when stream is not registered.
func relayGateway(c *core.Core) execution.RelayGateway {
	svc, ok := c.Registry.Get(stream.RelayGatewayKey)
	if !ok {
		return nil
	}
	gw, _ := svc.(execution.RelayGateway)
	return gw
}

// executionReader adapts the registered execution store onto the report sender's
// ExecutionReader (durability cut). Returns nil when the store is unavailable —
// already-queued reports still replay; only reconstruction of a lost accepted is
// skipped.
func executionReader(c *core.Core) ExecutionReader {
	store := executionStore(c)
	if store == nil {
		return nil
	}
	return executionStoreReader{store}
}

// executionStoreReader bridges *execution.ExecutionStore to ExecutionReader,
// projecting the durable row onto the accepted-report immutable facts via the
// same mapping the live enqueue path uses (acceptedRowFromExec), so a
// reconstructed accepted is byte-identical to the one enqueued at admission time.
type executionStoreReader struct{ store *execution.ExecutionStore }

func (r executionStoreReader) LoadAcceptedRow(execID string) (AcceptedRow, bool, error) {
	e, ok, err := r.store.GetByID(execID)
	if err != nil || !ok {
		return AcceptedRow{}, ok, err
	}
	return acceptedRowFromExec(e), true, nil
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
	// P.4: startup replay + periodic drain of the durable report outbox. The
	// first Flush inside Run replays any reports left unacked across a restart.
	if m.sender != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.sender.Run(ctx)
		}()
	}
	log.Println("[dispatch] consumer started")
	return nil
}

// Stop cancels the poll loop and waits for it to drain.
func (m *DispatchModule) Stop(_ context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}
	if m.outbox != nil {
		return m.outbox.Close()
	}
	return nil
}
