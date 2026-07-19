package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/module/stream"
	"github.com/wake/purdex/internal/relay"
)

// Environment keys for the Ploom connection. These are placeholder sourcing for
// M0 P.3 — the token/URL are read here but never hardcoded; a first-class config
// surface can replace this later without touching the client/worker.
const (
	envPloomURL   = "PDX_PLOOM_URL"
	envPloomToken = "PDX_PLOOM_TOKEN"
)

// ErrMissingDependency is returned by Init when a Ploom endpoint is configured
// but one of the consumer's mandatory internal dependencies is absent (or
// published under the wrong type).
//
// Why this is fatal rather than a degradation: claiming a dispatch (E2) is an
// irreversible move on Ploom's side, and M0 has no dispatch expiry. If the
// daemon cannot create a durable execution row AND queue its report chain, a
// claimed dispatch is wedged forever — Ploom holds it as claimed while nothing
// local exists to reconcile or report. So the consumer either has everything it
// needs to be accountable for a claim, or it must never claim at all.
var ErrMissingDependency = errors.New("dispatch dependency unavailable")

// DispatchModule wires the Ploom consumer: it polls pending dispatches, claims
// them, and fetches their full detail (M0 Task P.3). It stops at fetch — the
// FetchSink is the seam where P.7 attaches execution create → admission → launch
// → report. Depending on the execution module keeps that wiring ordering ready.
type DispatchModule struct {
	core       *core.Core
	client     *Client
	worker     *Worker
	sink       FetchSink
	outbox     *execution.Outbox
	sender     *Sender
	reconciler *execution.Reconciler

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
// builds the client, report sender, and consume→launch sink.
//
// Two distinct "off" states, deliberately different:
//   - No Ploom URL → the consumer is simply not configured. Nothing is claimed,
//     nothing is owed, and the daemon boots normally (M0 has no default endpoint).
//   - A Ploom URL IS configured but an internal dependency is missing → hard
//     error (ErrMissingDependency). Half a consumer would claim dispatches it can
//     never account for, so it must not exist at all.
func (m *DispatchModule) Init(c *core.Core) error {
	m.core = c

	baseURL := os.Getenv(envPloomURL)
	token := os.Getenv(envPloomToken)
	if baseURL == "" {
		return nil // consumer not configured — see Start
	}

	// Mandatory dependency gate (fail closed, BEFORE any client exists):
	//   - execution store: the durable runtime row + the report outbox living in
	//     the same database (atomic transition+report);
	//   - relay gateway: the launcher's probe/push path — without it nothing can
	//     ever reach running;
	//   - terminal seam: the only producer of the completed/failed report, so
	//     without it every launched execution wedges Ploom at running.
	store := executionStore(c)
	if store == nil {
		return fmt.Errorf("%w: execution store (registry key %q)", ErrMissingDependency, execution.RegistryKey)
	}
	gw := relayGateway(c)
	if gw == nil {
		return fmt.Errorf("%w: stream relay gateway (registry key %q)", ErrMissingDependency, stream.RelayGatewayKey)
	}
	seam := terminalSeam(c)
	if seam == nil {
		return fmt.Errorf("%w: stream terminal seam (registry key %q)", ErrMissingDependency, stream.TerminalSeamKey)
	}

	m.client = NewClient(baseURL, token)

	// P.4: durable report outbox + sender — built BEFORE the sink because the
	// P.7 launch reporter enqueues accepted/running through this sender. The
	// sender drains queued reports to Ploom with accepted-before-lifecycle
	// ordering, an ack cursor, and retry/backoff; the execution store is the
	// durability-cut source for reconstructing a lost accepted from its row.
	//
	// The outbox is a view over the EXECUTION database (execution owns the
	// report_outbox table), which is what makes "commit the state transition and
	// enqueue its report" a single atomic transaction.
	m.outbox = store.Outbox()
	m.sender = NewSender(m.outbox, m.client, WithExecutionReader(executionStoreReader{store}))

	// P.7: assemble the launch durable-cut sink (a test may pre-inject m.sink).
	if m.sink == nil {
		m.sink = m.consumeSink(m.buildCoordinator(c, store, gw))
	}

	// P.8: wire the terminal-outcome seam. The stream module publishes itself as
	// the seam (SetTerminalHandler + LastResult); on process exit the handler
	// classifies the outcome, captures the diff artifact, and enqueues the
	// completed/failed report through the same durable outbox.
	m.wireTerminal(store, seam, daemonID(c))

	// P.9: assemble the startup reconcile sweep + manual reclaim, over the
	// execution store (live rows), core tmux (session liveness probe + orphan
	// kill), and the durable terminal outbox adapter.
	m.reconciler = m.buildReconciler(c, store)

	m.worker = NewWorker(m.client, WithSink(m.sink))
	return nil
}

// buildReconciler assembles the P.9 Reconciler over the (already validated)
// execution store.
func (m *DispatchModule) buildReconciler(c *core.Core, store *execution.ExecutionStore) *execution.Reconciler {
	return execution.NewReconciler(store, c.Tmux, terminalReporter{}, execution.BuildDiffArtifact, daemonID(c))
}

// streamTerminalSeam is the narrow slice of the stream module the terminal wiring
// needs (satisfied by *stream.StreamModule): register the process-exit handler
// and read the captured `result` for outcome classification.
type streamTerminalSeam interface {
	SetTerminalHandler(h stream.TerminalHandler)
	LastResult(code string) (stream.ResultEvent, bool)
}

// wireTerminal registers the P.8 terminal handler on the stream seam (both are
// validated non-nil by Init). The handler resolves the execution by session_code,
// classifies the outcome (exit code + captured result), and enqueues the terminal
// report; it is idempotent (unknown/terminal/race → no report).
func (m *DispatchModule) wireTerminal(store *execution.ExecutionStore, seam streamTerminalSeam, daemonID string) {
	proc := execution.NewTerminalProcessor(store, terminalReporter{}, execution.BuildDiffArtifact, daemonID)
	seam.SetTerminalHandler(func(code string, ev relay.TerminalEvent) {
		res, ok := seam.LastResult(code)
		outcome := execution.ResultOutcome{HasResult: ok, IsError: res.IsError, Subtype: res.Subtype}
		if err := proc.Handle(context.Background(), code, ev.ExitCode, ev.Signaled, outcome); err != nil {
			log.Printf("[dispatch] terminal handle session=%s: %v", code, err)
		}
	})
}

// terminalSeam resolves the stream-published terminal-outcome seam, or nil when
// stream is not registered.
func terminalSeam(c *core.Core) streamTerminalSeam {
	svc, ok := c.Registry.Get(stream.TerminalSeamKey)
	if !ok {
		return nil
	}
	seam, _ := svc.(streamTerminalSeam)
	return seam
}

// daemonID returns the daemon's host id for scoping artifact pointers, falling
// back to a stable placeholder when unset.
func daemonID(c *core.Core) string {
	c.CfgMu.RLock()
	id := c.Cfg.HostID
	c.CfgMu.RUnlock()
	if id == "" {
		return "daemon"
	}
	return id
}

// buildCoordinator assembles the execution launch durable-cut Coordinator (P.6)
// over the dependencies Init already validated: the execution store (row store +
// launch fence), the stream relay gateway (probe/push relay), core tmux, and the
// live daemon connection config.
func (m *DispatchModule) buildCoordinator(c *core.Core, store *execution.ExecutionStore, gw execution.RelayGateway) *execution.Coordinator {
	launcher := execution.NewRealLauncher(c.Tmux, gw, session.EncodeSessionID, func() execution.LaunchConfig {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		return execution.LaunchConfig{Token: c.Cfg.Token, Port: c.Cfg.Port, Bind: c.Cfg.Bind}
	})
	// M0: no root containment (allowedRoots nil). A first-class allowlist config
	// can restrict admissible repos later without touching this seam.
	admitter := execution.NewAdmitter(store, nil)
	reporter := launchReporter{}
	return execution.NewCoordinator(admitter, store, reporter, launcher, hostSandboxPolicy(c))
}

// hostSandboxPolicy resolves the daemon's authoritative sandbox host policy from
// config (spec §8.1). Empty → least-privilege default (ask); a misconfigured
// value fails closed to the same strict default (logged) rather than silently
// widening — the daemon still boots.
func hostSandboxPolicy(c *core.Core) execution.Profile {
	c.CfgMu.RLock()
	raw := c.Cfg.Dispatch.SandboxHostPolicy
	c.CfgMu.RUnlock()
	policy, err := execution.ResolveHostPolicy(raw)
	if err != nil {
		log.Printf("[dispatch] invalid sandbox host policy %q: %v — defaulting to %s",
			raw, err, execution.DefaultHostPolicy)
		return execution.DefaultHostPolicy
	}
	return policy
}

// consumeSink is the P.7 tail of the consume loop: for each claimed+fetched
// dispatch it runs the launch durable cut (admission → row → accepted → launch →
// running).
//
// EVERY Accept failure funnels into Coordinator.Reject, which is the single
// answer to "the dispatch was claimed, so Ploom must hear something":
//   - admission rejection (repo busy / bad repo_location / unknown profile) —
//     no row was ever created, so Reject creates the terminal one that backs the
//     accepted+failed projection;
//   - launch failure — the Coordinator already marked its row failed and queued
//     both reports, so Reject is an idempotent no-op on the existing dispatch_id;
//   - store failure before any row exists (e.g. the accepted enqueue aborted the
//     insert transaction) — Reject is the only remaining chance to tell Ploom,
//     since ListLive-based reconcile has no row to find.
func (m *DispatchModule) consumeSink(coord *execution.Coordinator) FetchSink {
	return func(ctx context.Context, cd ClaimedDispatch) {
		req := execution.LaunchRequest{
			DispatchID:       cd.Pending.DispatchID,
			RepoLocation:     cd.Detail.RepoLocation.LocalDir,
			RepoLocationJSON: marshalRepoLocation(cd.Detail.RepoLocation),
			Prompt:           buildPrompt(cd.Detail.Issue),
			SandboxProfile:   cd.Detail.SandboxProfile,
		}
		if _, err := coord.Accept(ctx, req); err != nil {
			code := rejectionCode(err)
			log.Printf("[dispatch] dispatch=%s not launched (%s): %v", req.DispatchID, code, err)
			if _, rerr := coord.Reject(ctx, req, code, err); rerr != nil {
				log.Printf("[dispatch] record rejection dispatch=%s: %v", req.DispatchID, rerr)
			}
		}
	}
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

// RegisterRoutes exposes the P.9 manual-reclaim endpoint when reconcile is wired;
// the poll/claim/fetch consumer itself is pull-only. Auth is the global TokenAuth
// middleware's job (this mux is wrapped by it in main).
func (m *DispatchModule) RegisterRoutes(mux *http.ServeMux) {
	if m.reconciler != nil {
		mux.HandleFunc("POST /api/dispatch/reclaim", m.reconciler.ReclaimHandler())
	}
}

// Start launches the poll loop in the background when a Ploom endpoint is
// configured; otherwise it logs that the consumer is disabled.
func (m *DispatchModule) Start(_ context.Context) error {
	// No worker means Init either found no Ploom endpoint or failed closed on a
	// missing dependency. Either way nothing may poll or claim.
	if m.worker == nil {
		log.Printf("[dispatch] consumer not started (%s unset or dependencies unavailable)", envPloomURL)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	// P.9: run the startup reconcile sweep once before the poll loop drains new
	// dispatches, so any execution wedged by a prior crash is driven terminal —
	// unblocking its repo (admission is status-based) and enqueuing the recovered
	// terminal report for the sender to replay. Best-effort: a sweep error must
	// not stop the consumer from starting.
	if m.reconciler != nil {
		if err := m.reconciler.Reconcile(ctx); err != nil {
			log.Printf("[dispatch] startup reconcile: %v", err)
		}
	}

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
	// The outbox is a view over the execution module's database — that module
	// owns its lifetime and closes it.
	return nil
}
