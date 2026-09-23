// Package peers implements the "peers" daemon module: GET /api/peers, a
// local inventory of this host's tmux sessions joined with the agent module's
// owner resolution and the Claude Code session registry; the peer-host
// management and settings routes; POST /api/peers/hosts/{alias}/verify
// (hosts_verify.go) probes one entry the way a scope=all row does; POST
// /api/peers/send, the outbound half
// of cross-host messaging (send.go); POST /api/peers/deliver, the
// inbound half (deliver.go), backed by the per-origin helper manager
// (helpers.go) and the peer_messages audit store; the reply path that
// forwards a target's native reply back through the return route, and
// GET /api/peers/log over the audit store (reply.go). Start/Stop live in
// lifecycle.go.
package peers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper"
	"github.com/wake/purdex/internal/store"
)

// fetchFunc is the fan-out seam: fetchRemote in production, a fake in tests.
type fetchFunc func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error)

// postDeliverFunc is the outbound seam: one POST /api/peers/deliver to a
// remote daemon. postDeliver (send.go) in production, a fake in tests; the
// send path and the reply path (Task 9) both call it.
type postDeliverFunc func(ctx context.Context, client *http.Client, baseURL, bearer string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error)

// writeFrameFunc is the inbox-socket seam: ccuds.WriteFrame in production.
type writeFrameFunc func(ctx context.Context, sock string, line []byte, timeout time.Duration) error

// AuditStore is the peer_messages audit trail the module writes:
// *store.PeerMessageStore in production, a fake in tests. A nil AuditStore
// makes every send/deliver fail with audit_unavailable — the audit row is
// written BEFORE anything reaches a socket and a delivery that cannot be
// recorded is not made.
type AuditStore interface {
	Insert(store.PeerMessage) (int64, error)
	SetResult(id int64, effectiveMode, result, errText string) error
	Tail(n int) ([]store.PeerMessage, error)
}

// helperSockDir is where `pdx peer-proxy` helpers bind their sockets:
// the same directory Claude Code itself uses, so the reply address a
// frame carries is one the harness can dial.
const helperSockDir = "/tmp/cc-socks"

// remoteFetchTimeout bounds each individual host fetch in a scope=all
// fan-out, independent of the shared http.Client's own timeout.
const remoteFetchTimeout = 3 * time.Second

// maxRemoteTextBytes bounds how much of a remote peer's attacker-controlled
// text (an error message, or a mismatched host_id) is allowed to appear in
// this host's own error responses and rows, via boundRemoteText.
const maxRemoteTextBytes = 200

// maxRemoteRowsBytes bounds the JSON-encoded size of one remote host's
// Peers list in a scope=all fan-out row. Every row is attacker-controlled
// (any configured peer host), and the aggregate scope=all response has no
// bound of its own beyond the CLI's overall 16 MiB response cap — without
// a per-host cap, a single misbehaving or malicious peer returning a huge
// inventory (e.g. one record with a multi-MB cwd) could push the whole
// response past that cap and take down every other host's rows with it.
const maxRemoteRowsBytes = 1 << 20 // 1 MiB

// boundRemoteText truncates s to at most maxRemoteTextBytes bytes, cutting
// on a rune boundary so the result is always valid UTF-8, and appends "…"
// when truncation actually removed something. Used to bound remote peer
// text (an env.Error or a mismatched host_id) before it is embedded in an
// error message returned to a caller or persisted/logged locally — a
// misbehaving or malicious peer should not be able to inflate or pollute
// this host's own responses via an unbounded string.
func boundRemoteText(s string) string {
	if len(s) <= maxRemoteTextBytes {
		return s
	}
	cut := maxRemoteTextBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// redactSecret replaces every occurrence of secret in s with "[redacted]".
// fetchHostResult applies it, with the entry's OWN outbound token, to every
// remote-derived string: a malicious peer receives that token as our Bearer
// and could otherwise echo it back into a row that the CLI prints, the
// verify route returns and the Peers page renders (#1152). The peer
// already holds the token, so this hides nothing from it; it keeps our own
// admin surfaces from displaying a value the API never returns.
func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// boundRemote is redactSecret then boundRemoteText: the one shape every
// peer-controlled string takes before it reaches a caller, an audit row or
// the log, with the outbound token of the entry that was dialled (#1152).
// Redaction runs first so a token straddling the truncation point cannot
// survive as a prefix.
func boundRemote(s, token string) string {
	return boundRemoteText(redactSecret(s, token))
}

// redactRecord scrubs secret from every string field a peer row carries —
// Peers rows are the peer's own text as much as its Error is.
func redactRecord(rec *ipeers.PeerRecord, secret string) {
	if secret == "" {
		return
	}
	rec.Host = redactSecret(rec.Host, secret)
	rec.HostID = redactSecret(rec.HostID, secret)
	rec.Address = redactSecret(rec.Address, secret)
	rec.RowKind = redactSecret(rec.RowKind, secret)
	rec.Ref = redactSecret(rec.Ref, secret)
	rec.Title = redactSecret(rec.Title, secret)
	rec.TitleSource = redactSecret(rec.TitleSource, secret)
	rec.SessionCode = redactSecret(rec.SessionCode, secret)
	rec.SessionName = redactSecret(rec.SessionName, secret)
	rec.TmuxInstance = redactSecret(rec.TmuxInstance, secret)
	rec.TmuxName = redactSecret(rec.TmuxName, secret)
	rec.Cwd = redactSecret(rec.Cwd, secret)
	rec.Reason = redactSecret(rec.Reason, secret)
	if rec.Agent != nil {
		a := *rec.Agent
		a.Type = redactSecret(a.Type, secret)
		a.SessionID = redactSecret(a.SessionID, secret)
		a.PeerName = redactSecret(a.PeerName, secret)
		a.ProcStart = redactSecret(a.ProcStart, secret)
		a.Inbox = redactSecret(a.Inbox, secret)
		a.Status = redactSecret(a.Status, secret)
		a.Version = redactSecret(a.Version, secret)
		rec.Agent = &a
	}
}

// writeWireError writes e as the JSON body of a 4xx/5xx answer on the
// messaging routes (/send, /deliver): every such body is an ipeers.APIError.
func writeWireError(w http.ResponseWriter, status int, e ipeers.APIError) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// configSnapshot is the one config read a request makes, under RLock: the
// local identity and a clone of the peer hosts, so a concurrent config
// mutation cannot be observed mid-request.
type configSnapshot struct {
	hostID string
	alias  string
	hosts  []config.PeerHost
}

func (m *Module) configSnapshot() configSnapshot {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return configSnapshot{
		hostID: m.core.Cfg.HostID,
		alias:  m.core.Cfg.PeerAlias(),
		hosts:  append([]config.PeerHost(nil), m.core.Cfg.Peers.Hosts...),
	}
}

// Module implements core.Module for GET /api/peers.
type Module struct {
	core        *core.Core
	sessions    session.SessionProvider
	owners      agent.OwnerResolver
	registryDir string          // default filepath.Join(home, ".claude", "sessions")
	liveness    ipeers.Liveness // default ipeers.DefaultLiveness()
	budget      time.Duration   // default 2 * time.Second
	now         func() time.Time
	client      *http.Client                     // default newRemoteClient(); shared across fan-out fetches
	fetch       fetchFunc                        // default fetchRemote; test seam
	logf        func(format string, args ...any) // default log.Printf; test seam

	// titles is the peer_labels store (Task 3/7): Snapshot joins into every
	// inventory build (localEnvelope, unguarded — a plain read with no
	// ordering requirement of its own); the self routes (titles.go —
	// whoami, claim, release) read and write it under titleMu, held across
	// the whole verb (origin/registry read through the store call and the
	// response construction), even whoami's own Snapshot-only read, so a
	// concurrent claim/release can never interleave with it.
	titles  TitleStore
	titleMu sync.Mutex

	// Rotation record (rotation.go): alias → the peer's most recent inbound
	// authentication, in memory.
	rotMu       sync.Mutex
	lastInbound map[string]inboundAuth
	// rotateAfterGate is a test seam: called by the commit/cancel handlers
	// right after their gate check, still inside the UpdateConfig closure
	// and still holding rotMu (nil in production).
	rotateAfterGate func()

	// Inbound delivery (deliver.go) and its collaborators.
	audit            AuditStore     // nil ⇒ audit_unavailable on every deliver
	helpers          *helperManager // built in Init (production) or by the test fixture
	dedup            *dedupSet      // msg_id window, ipeers.DedupWindow
	pairs            *pairLimiter   // per (sender, receiver) process pair, ipeers.PairRateLimit
	hostLimit        *hostLimiter   // per authenticated host, ipeers.HostRateLimit; before decode/dedup/audit/inventory
	writeFrame       writeFrameFunc // default ccuds.WriteFrame
	sockWriteTimeout time.Duration  // default ipeers.SocketWriteTimeout
	newMsgID         func() string  // default uuid v4 (crypto/rand); the reply path mints ids with it

	// Outbound delivery (send.go; the reply path reuses both).
	deliverClient *http.Client    // default newDeliverClient() (Init); one InterDaemonTimeout per call
	post          postDeliverFunc // default postDeliver; test seam

	// Lifecycle. stopCtx is cancelled first in Stop: handlers answer 503
	// not_ready, in-flight socket writes abort, and the reply semaphore
	// wait gives up. reapWG joins the idle-reap ticker goroutine; workers
	// joins the reply workers (Task 9); replySem bounds them.
	stopCtx    context.Context
	stopCancel context.CancelFunc
	reapWG     sync.WaitGroup
	workers    sync.WaitGroup
	replySem   chan struct{}

	// warnedVersions dedupes the one-shot "Claude Code newer than verified"
	// log line (localEnvelope) by version string: sync.Map since it is
	// read/written from concurrent request handlers with no other lock
	// guarding it. Zero value is ready to use.
	warnedVersions sync.Map

	// putHostAfterSnapshot is a test seam: called by handlePutHost right
	// after its pre-lock snapshot, so a test can force a concurrent
	// mutation into that window. No-op in production.
	putHostAfterSnapshot func()
}

// New constructs a peers Module with production defaults over audit (the
// meta store's PeerMessages; nil disables delivery with audit_unavailable)
// and titles (the meta store's PeerLabels; nil means every conversation has
// no title at all and claims fail with store_unavailable). Collaborators
// (sessions, owners) and the helper manager are wired in Init: the manager
// needs the config's data dir and the daemon's own executable path, neither
// of which belongs in a constructor.
func New(audit AuditStore, titles TitleStore) *Module {
	registryDir := filepath.Join(".claude", "sessions")
	if home, err := os.UserHomeDir(); err == nil {
		registryDir = filepath.Join(home, ".claude", "sessions")
	}
	stopCtx, stopCancel := context.WithCancel(context.Background())
	m := &Module{
		registryDir:          registryDir,
		liveness:             ipeers.DefaultLiveness(),
		budget:               2 * time.Second,
		now:                  time.Now, // the one clock seam; every other clock reader below takes m.now
		client:               newRemoteClient(),
		fetch:                fetchRemote,
		logf:                 log.Printf,
		audit:                audit,
		titles:               titles,
		writeFrame:           ccuds.WriteFrame,
		sockWriteTimeout:     ipeers.SocketWriteTimeout,
		newMsgID:             uuid.NewString,
		post:                 postDeliver,
		stopCtx:              stopCtx,
		stopCancel:           stopCancel,
		replySem:             make(chan struct{}, replyWorkerCap),
		putHostAfterSnapshot: func() {},
	}
	m.dedup = newDedupSet(ipeers.DedupWindow, m.now)
	m.pairs = newPairLimiter(ipeers.PairRateLimit, ipeers.PairRateWindow, m.now)
	m.hostLimit = newHostLimiter(ipeers.HostRateLimit, ipeers.HostRateWindow, m.now)
	return m
}

func (m *Module) Name() string           { return "peers" }
func (m *Module) Dependencies() []string { return []string{"session", "agent"} }

// Init wires the session provider and owner resolver from the service
// registry. Both are required: unlike some modules' soft-degrade Init, a
// missing dependency here is a hard error naming the missing registry key,
// since GET /api/peers has no meaningful behavior without either.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	// The rotation record's authoritative observer: the peer auth matcher
	// calls it under CfgMu.RLock for every host-token match (rotation.go).
	c.HostAuthObserver = m.noteInboundFP

	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		return fmt.Errorf("peers: service %q not registered", session.RegistryKey)
	}
	sessions, ok := svc.(session.SessionProvider)
	if !ok {
		return fmt.Errorf("peers: service %q does not implement session.SessionProvider (%T)", session.RegistryKey, svc)
	}
	m.sessions = sessions

	svc, ok = c.Registry.Get(agent.OwnerResolverKey)
	if !ok {
		return fmt.Errorf("peers: service %q not registered", agent.OwnerResolverKey)
	}
	owners, ok := svc.(agent.OwnerResolver)
	if !ok {
		return fmt.Errorf("peers: service %q does not implement agent.OwnerResolver (%T)", agent.OwnerResolverKey, svc)
	}
	m.owners = owners

	m.deliverClient = newDeliverClient()

	// The helper manager: one `pdx peer-proxy` per remote sender, spawned
	// from this daemon's own executable (resolved here, not in New — tests
	// never spawn the real binary), owned durably in <data_dir>/proxies.json.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("peers: resolve own executable for peer-proxy helpers: %w", err)
	}
	m.helpers = newHelperManager(helperManagerConfig{
		Start:       proxyhelper.ExecStarter(exe, log.Writer()),
		ProxiesPath: filepath.Join(c.Cfg.DataDir, "proxies.json"),
		RegistryDir: m.registryDir,
		SockDir:     helperSockDir,
		Version:     ccuds.VerifiedCCVersion,
		Now:         m.now,
		ProcStart:   ccuds.DefaultProcStart,
		LiveEntries: func() []ipeers.Entry {
			entries, _, err := ipeers.ReadRegistry(m.registryDir, m.liveness)
			if err != nil {
				m.logf("peers: read registry for helper peer features: %v", err)
				return nil
			}
			return entries
		},
		OnFrame: m.handleReplyFrame,
		Log:     m.logf,
	})

	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/peers", m.handlePeers)
	mux.HandleFunc("GET /api/peers/hosts", m.handleListHosts)
	mux.HandleFunc("POST /api/peers/hosts", m.handleAddHost)
	mux.HandleFunc("PUT /api/peers/hosts/{alias}", m.handlePutHost)
	mux.HandleFunc("DELETE /api/peers/hosts/{alias}", m.handleDeleteHost)
	mux.HandleFunc("POST /api/peers/hosts/{alias}/verify", m.handleVerifyHost)
	mux.HandleFunc("POST /api/peers/hosts/{alias}/rotate", m.handleRotateHost)
	mux.HandleFunc("POST /api/peers/hosts/{alias}/rotate/commit", m.handleRotateCommit)
	mux.HandleFunc("POST /api/peers/hosts/{alias}/rotate/cancel", m.handleRotateCancel)
	mux.HandleFunc("GET /api/peers/settings", m.handleGetSettings)
	mux.HandleFunc("PUT /api/peers/settings", m.handlePutSettings)
	mux.HandleFunc("POST /api/peers/send", m.handleSend)
	mux.HandleFunc("POST /api/peers/deliver", m.handleDeliver)
	mux.HandleFunc("GET /api/peers/log", m.handlePeersLog)
	mux.HandleFunc("POST /api/peers/self", m.handleSelf)
	mux.HandleFunc("PUT /api/peers/self/title", m.handleClaimTitle)
	mux.HandleFunc("DELETE /api/peers/self/title", m.handleReleaseTitle)
}

// handlePeers serves GET /api/peers. scope unset/"local" returns this
// host's local inventory only; scope=all fans out to every configured peer
// host in parallel (admin principal only — see policy.go's
// HostRoutePolicy, enforced again here in depth); any other scope is 400.
func (m *Module) handlePeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Spec §6.2: a host principal that reached this handler authenticated
	// with one of its entry's two inbound tokens; remember which, before
	// any refusal below.
	if p, ok := middleware.PrincipalFrom(r.Context()); ok {
		m.noteInboundAuth(p)
	}

	scope := r.URL.Query().Get("scope")
	switch scope {
	case "", "local":
		// fine, snapshot below covers this path too.
	case "all":
		principal, ok := middleware.PrincipalFrom(r.Context())
		if !ok || principal.Kind != middleware.PrincipalAdmin {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": ipeers.ErrForbidden})
			return
		}
	default:
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "unknown scope"})
		return
	}

	// One config snapshot up front, used by every path below (local-only or
	// scope=all) — in particular, so the local HostResult{Alias, HostID}
	// row can never disagree with the host/host_id embedded in its own
	// Peers records.
	snap := m.configSnapshot()

	if scope != "all" {
		json.NewEncoder(w).Encode(m.localEnvelope(r.Context(), snap.hostID, snap.alias))
		return
	}

	json.NewEncoder(w).Encode(m.allEnvelope(r.Context(), snap.hostID, snap.alias, snap.hosts))
}

// contextSessionLister is the context-aware session list the production
// provider (*session.SessionModule) offers alongside SessionProvider. It is an
// optional interface — asserted, as the agent module does for
// LookupCodeByName — so the SessionProvider contract (and its many fakes)
// stays unchanged.
type contextSessionLister interface {
	ListSessionsContext(ctx context.Context) ([]session.SessionInfo, error)
}

// contextTmuxInstancer is the context-aware tmux-instance probe, optional in
// the same way as contextSessionLister.
type contextTmuxInstancer interface {
	TmuxInstanceContext(ctx context.Context) string
}

// The production provider offers both; a rename there must not silently
// drop the inventory back to unbounded reads.
var (
	_ contextSessionLister = (*session.SessionModule)(nil)
	_ contextTmuxInstancer = (*session.SessionModule)(nil)
)

// listSessionsWithin reads the session list under ctx — the inventory's
// budget context (#1293 §3.2): a hung tmux read ends at the budget with an
// error, and the inventory answers ok:false instead of holding the request.
// A provider without ListSessionsContext is read unbounded here (the session
// module still caps its own read).
func (m *Module) listSessionsWithin(ctx context.Context) ([]session.SessionInfo, error) {
	lister, ok := m.sessions.(contextSessionLister)
	if !ok {
		return m.sessions.ListSessions()
	}
	return lister.ListSessionsContext(ctx)
}

// tmuxInstanceWithin probes the tmux generation under ctx (the inventory's
// budget context); "" when the probe fails or ctx ends first. A provider
// without TmuxInstanceContext is probed unbounded here (the probe keeps its
// own cap).
func (m *Module) tmuxInstanceWithin(ctx context.Context) string {
	if p, ok := m.sessions.(contextTmuxInstancer); ok {
		return p.TmuxInstanceContext(ctx)
	}
	return m.sessions.TmuxInstance()
}

// localEnvelope builds this host's own inventory from a caller-supplied
// hostID/alias: the response body for scope unset/"local", and the local
// row's peers/ok/partial/error for scope=all. It never touches CfgMu itself
// — the caller (handlePeers) takes the one config snapshot for the whole
// request, so the local row's titles and the host/host_id embedded in its
// own Peers records are always built from the same values.
func (m *Module) localEnvelope(ctx context.Context, hostID, alias string) ipeers.Envelope {
	// ONE budget for the whole local inventory (#1293 §3.2): both tmux-
	// instance probes, the session list and every owner lookup run under
	// invCtx, so a hung tmux costs the budget once, not once per read. It is
	// wall-clock (context.WithTimeout), not m.now: m.now is the owner loop's
	// clock and may be a test clock, which the per-session check below keeps
	// using.
	invCtx, cancel := context.WithTimeout(ctx, m.budget)
	defer cancel()
	deadline := m.now().Add(m.budget)

	writeError := func(errMsg string) ipeers.Envelope {
		return ipeers.Envelope{
			HostID:               hostID,
			Alias:                alias,
			OK:                   false,
			Error:                errMsg,
			Partial:              false,
			Peers:                []ipeers.PeerRecord{},
			DaemonVersion:        buildinfo.Version,
			UnknownRegistryFiles: []string{},
		}
	}

	instance := m.tmuxInstanceWithin(invCtx)

	sessions, err := m.listSessionsWithin(invCtx)
	if err != nil {
		return writeError(err.Error())
	}

	entries, diag, err := ipeers.ReadRegistryDiag(m.registryDir, m.liveness)
	if err != nil {
		return writeError(err.Error())
	}
	m.warnNewerCCVersions(entries)

	summaries := make([]ipeers.SessionSummary, 0, len(sessions))
	owners := make(map[string]ipeers.Owner, len(sessions))
	unresolved := make(map[string]bool)

	for _, s := range sessions {
		summaries = append(summaries, ipeers.SessionSummary{
			Code:         s.Code,
			Name:         s.Name,
			Cwd:          s.Cwd,
			TmuxInstance: s.TmuxInstance,
		})

		if !m.now().Before(deadline) {
			unresolved[s.Code] = true
			continue
		}

		owner, ok, err := m.owners.ResolveSessionOwner(invCtx, s.Code)
		if err != nil {
			// The lookup itself failed (tmux read error, resolver timeout,
			// cancelled context) — this is not "no agent". Reporting it as
			// no_agent would tell the SPA a live session has none, so it is
			// reported the same way as a session whose owner lookup never
			// ran: agent:null, reason:"", partial:true (Item 1, #988).
			unresolved[s.Code] = true
			continue
		}
		if ok {
			owners[s.Code] = ipeers.Owner{
				AgentType:  owner.AgentType,
				SessionID:  owner.SessionID,
				Cwd:        owner.Cwd,
				TmuxPaneID: owner.TmuxPaneID,
				LastSeenAt: owner.LastSeenAt,
				Status:     owner.Status,
			}
		}
	}

	// Mirrors handleSessionProvenance (internal/module/agent): the tmux
	// generation is sampled on both sides of the (slow) owner-resolution
	// work. A tmux server that restarted mid-inventory would otherwise let
	// session/owner data straddle two different tmux generations into one
	// answer. Either sample being "" (unknown) means the check cannot fire,
	// and the response proceeds as if nothing had changed.
	if after := m.tmuxInstanceWithin(invCtx); instance != "" && after != "" && after != instance {
		return writeError("tmux server restarted during inventory")
	}

	// Registry diagnosis (spec §3.3): an alive-but-undecodable file could
	// still be a live session whose registry entry just failed to parse —
	// it blocks nothing it isn't already blocking (label claims, Task 7),
	// but it means this inventory cannot swear to a NEGATIVE about any
	// tuple, so it marks the whole response partial the same way an
	// unresolved owner lookup does.
	unknown := diag.BlockingUnknown()
	if unknown == nil {
		unknown = []string{}
	}

	// The title snapshot (Task 3's peer_labels table) is joined the same
	// way: a nil store or a read failure never blocks the inventory build
	// (every row still gets its address, which is derived from the
	// registry and owes the store nothing), but a failed read is reported
	// the same way a failed owner lookup is — this response is showing a
	// blank title column it cannot vouch for — and signalled on its own as
	// titles_unavailable, so a consumer (pdx peers, the SPA) names the
	// cause instead of inferring it from the absence of the other two
	// partial causes.
	titles, titlesErr := m.titleSnapshot()
	if titlesErr != nil {
		m.logf("peers: inventory: title store unavailable, reporting rows without titles: %v", titlesErr)
	}
	titlesUnavailable := titlesErr != nil

	partial := len(unresolved) > 0 || len(unknown) > 0 || titlesUnavailable

	// This daemon's own helpers are hidden as proxy rows by pid (their
	// registry entries are otherwise indistinguishable from a Claude Code
	// session); another daemon's helpers are recognised by argv (D9).
	proxyPIDs := map[int]bool{}
	if m.helpers != nil {
		proxyPIDs = m.helpers.ProxyPIDs()
	}

	peerRecords := ipeers.Build(ipeers.BuildInput{
		HostID:     hostID,
		Alias:      alias,
		Sessions:   summaries,
		Owners:     owners,
		Unresolved: unresolved,
		Entries:    entries,
		ProxyPIDs:  proxyPIDs,
		Titles:     titles,
		// An empty title map means "unreadable", not "no user titles".
		// Build does not branch on this: it is passed through so the flag
		// travels with the rows it explains, telling a consumer why their
		// title column is blank. It says nothing about their addresses,
		// which the title store never had a part in.
		TitlesUnavailable: titlesUnavailable,
	})

	return ipeers.Envelope{
		HostID: hostID,
		// The caller's snapshot alias, published so a peer pairing with this
		// host can adopt the name this host uses for itself (spec §7) rather
		// than inventing a local one that makes addresses unportable.
		Alias:                alias,
		OK:                   true,
		Partial:              partial,
		Peers:                peerRecords,
		DaemonVersion:        buildinfo.Version,
		UnknownRegistryFiles: unknown,
		TitlesUnavailable:    titlesUnavailable,
	}
}

// titleSnapshot reads the title table into Build's map. A nil store is an
// empty map and no error (Peer Address v2 has never been configured with a
// title store, which is not this inventory's trouble); a read error is
// returned so the caller can mark the response partial — the rows it
// renders below then carry no title at all, same as an absent row.
func (m *Module) titleSnapshot() (map[string]ipeers.TitleInfo, error) {
	out := map[string]ipeers.TitleInfo{}
	if m.titles == nil {
		return out, nil
	}
	rows, err := m.titles.Snapshot()
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out[r.SessionID] = ipeers.TitleInfo{Title: r.Label, Rev: r.Rev}
	}
	return out, nil
}

// warnNewerCCVersions logs a one-shot warning for every distinct Claude
// Code version among entries that is newer than ccuds.VerifiedCCVersion —
// the version every byte layout in the ccuds package was measured against,
// so a newer one reporting is where a silent protocol change would first
// show up. Deduped by version string in m.warnedVersions so the same
// version logs at most once per process lifetime, however many
// /api/peers calls (or entries sharing that version) see it.
func (m *Module) warnNewerCCVersions(entries []ipeers.Entry) {
	for _, e := range entries {
		if e.Version == "" || !ccuds.NewerThanVerified(e.Version) {
			continue
		}
		if _, alreadyWarned := m.warnedVersions.LoadOrStore(e.Version, struct{}{}); alreadyWarned {
			continue
		}
		m.logf("peers: Claude Code %s is newer than the last verified %s; run pdx msg selftest", e.Version, ccuds.VerifiedCCVersion)
	}
}

// allEnvelope builds a scope=all response: the local row first (from
// localEnvelope, labeled with the snapshot's alias/host_id), then every
// configured host in order, fetched in parallel — each write lands by index
// into a pre-sized slice so the result order matches config order
// regardless of which goroutine finishes first.
func (m *Module) allEnvelope(ctx context.Context, hostID, alias string, hosts []config.PeerHost) ipeers.AllEnvelope {
	results := make([]ipeers.HostResult, len(hosts)+1)

	// Start every remote fetch first so it overlaps with the (potentially
	// slow, owner-resolution-bound) local inventory build below, rather
	// than paying for the two sequentially.
	var wg sync.WaitGroup
	for i, h := range hosts {
		i, h := i, h
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i+1] = m.fetchHostResult(ctx, h)
		}()
	}

	local := m.localEnvelope(ctx, hostID, alias)
	results[0] = ipeers.HostResult{
		Alias: alias,
		// The local row's SelfAlias comes from the same snapshot alias as
		// Alias, so this host agrees with itself by construction and can
		// never be flagged as drifting — there is no second opinion to
		// disagree with, and inventing one from the live config would only
		// manufacture drift out of a mid-request rename.
		SelfAlias:            alias,
		HostID:               hostID,
		OK:                   local.OK,
		Error:                local.Error,
		Partial:              local.Partial,
		Peers:                local.Peers,
		DaemonVersion:        local.DaemonVersion,
		UnknownRegistryFiles: local.UnknownRegistryFiles,
		TitlesUnavailable:    local.TitlesUnavailable,
	}

	wg.Wait()

	return ipeers.AllEnvelope{Hosts: results}
}

// normalizeRemoteRows rewrites every row of a remote host's fan-out
// response into this host's local view: Host becomes alias (how WE have
// the peer configured, never the remote's own self-reported value), HostID
// becomes hostID (the caller's already-verified/bounded value for this
// host), and Address is RECOMPUTED by remoteAddress from the row's own
// fields. Exported at the package level (not a method) so P3 can reuse it
// as-is.
//
// It recomputes rather than adjusts because a paired peer is
// attacker-controlled — the same assumption fetchHostResult already makes
// about its error text and its host_id. The previous version kept everything
// after the remote's first "/" and re-prefixed the local alias, so the remote
// chose what this host printed in its ADDRESS column: publishing peer_name
// "trusted:ops" with address "air/trusted:ops" got `pdx peers --all` to render
// "air/trusted:ops [q34psn]", a pasteable address for a name spec §5.2 says
// can never be one.
func normalizeRemoteRows(rows []ipeers.PeerRecord, alias, hostID string) []ipeers.PeerRecord {
	out := make([]ipeers.PeerRecord, len(rows))
	for i, rec := range rows {
		rec.Host = alias
		rec.HostID = hostID
		rec.Address = remoteAddress(rec, alias)
		out[i] = rec
	}
	return out
}

// remoteAddress derives the address this host will print for one remote row,
// from that row's own fields and in the same order applyIdentity uses for a
// local one: a live cc entry with a routable name, else that entry's ref, else
// the tmux form of a session this host can actually address.
//
// The remote's own Address is not read at all, by design: it is the one field
// whose body carried whatever the remote wanted rendered, and a rule that
// consults it — even only for its shape — is a rule a reader has to check the
// remote against. Every input below is still the remote's, but each is put
// through the grammar this host routes by, so whatever is printed is a form
// Resolve can be handed back and will land on this very row.
//
// A row yielding none of the three forms gets "" rather than an unusable
// value, exactly as a rebuilt address that failed SplitAddress used to.
// A proxy row is one of those: its local "<host>/cc:<name>" form is retired
// and unresolvable, so reproducing it here would only put a remote-chosen
// string in the address column.
func remoteAddress(rec ipeers.PeerRecord, alias string) string {
	var session string
	switch {
	// hasLiveEntry's condition, spelled out: Resolve's name and ref tiers
	// decide only on rows carrying a real live cc registry entry, so only
	// those two forms may be printed for one.
	case rec.Agent != nil && rec.Agent.Type == "cc" && rec.Agent.PID != 0:
		switch {
		case ipeers.RoutableName(rec.Agent.PeerName):
			session = rec.Agent.PeerName
		case ipeers.IsRef(rec.Ref):
			session = rec.Ref
		}
	// Any other row: the tmux form, which names a place rather than a
	// conversation and is matched on SessionName — the same field printed
	// here, so the address resolves back to this row.
	case rec.SessionName != "":
		session = ipeers.LabelReservedTmux + ":" + rec.SessionName
	}
	if session == "" {
		return ""
	}
	addr := alias + "/" + session
	// The backstop the rebuild always had: a value that cannot be split back
	// into a host and a session is not an address, whatever produced it.
	if _, _, ok := ipeers.SplitAddress(addr); !ok {
		return ""
	}
	return addr
}

// fetchHostResult fetches one configured peer host's inventory for a
// scope=all fan-out, translating every failure mode (no outbound token,
// transport/decode error, host_id mismatch) into a failed HostResult rather
// than propagating an error.
func (m *Module) fetchHostResult(ctx context.Context, h config.PeerHost) ipeers.HostResult {
	if h.Token == "" {
		return ipeers.HostResult{
			Alias:                h.Alias,
			HostID:               h.HostID,
			OK:                   false,
			Error:                "no outbound token",
			Peers:                []ipeers.PeerRecord{},
			UnknownRegistryFiles: []string{},
		}
	}

	// bound redacts this entry's own outbound token from a remote-derived
	// string before truncating it: every string the peer controls (an
	// error message, a mismatched host_id, its self-reported alias/version,
	// the unknown-registry-files list) flows through this before it can
	// reach a row, a verify response or a returned error (#1152).
	bound := func(s string) string { return boundRemote(s, h.Token) }

	fetchCtx, cancel := context.WithTimeout(ctx, remoteFetchTimeout)
	defer cancel()

	env, err := m.fetch(fetchCtx, m.client, h.URL, h.Token)
	if err != nil {
		return ipeers.HostResult{
			Alias:                h.Alias,
			HostID:               h.HostID,
			OK:                   false,
			Error:                bound(err.Error()),
			Peers:                []ipeers.PeerRecord{},
			UnknownRegistryFiles: []string{},
		}
	}

	if h.HostID != "" && env.HostID != h.HostID {
		return ipeers.HostResult{
			Alias:                h.Alias,
			HostID:               h.HostID,
			OK:                   false,
			Error:                fmt.Sprintf("host_id mismatch: got %s", bound(env.HostID)),
			Peers:                []ipeers.PeerRecord{},
			UnknownRegistryFiles: []string{},
		}
	}

	// h.HostID is our own configured (trusted) value; env.HostID, used only
	// as a fallback for an unpaired host, is the remote's own report and
	// just as attacker-controlled as its Error text — so it is trusted
	// only when it passes the same validHostID check POST/PUT require
	// before ever persisting a host_id, exactly as verifyHost does.
	resultHostID := h.HostID
	if resultHostID == "" {
		if !validHostID(env.HostID) {
			return ipeers.HostResult{
				Alias:                h.Alias,
				HostID:               h.HostID,
				OK:                   false,
				Error:                "peer returned an invalid host_id",
				Peers:                []ipeers.PeerRecord{},
				UnknownRegistryFiles: []string{},
			}
		}
		resultHostID = env.HostID
	}

	peers := normalizeRemoteRows(env.Peers, h.Alias, resultHostID)
	for i := range peers {
		redactRecord(&peers[i], h.Token)
	}

	// A single misbehaving/malicious peer host must not be able to inflate
	// the whole scope=all aggregate past the CLI's own 16 MiB response
	// cap. Re-encode just this host's rows and, if they alone already
	// exceed maxRemoteRowsBytes, drop them and report a bounded failure
	// row instead — every OTHER host's row (and the local one) stays
	// intact regardless of what this one host returned.
	if encoded, err := json.Marshal(peers); err == nil && len(encoded) > maxRemoteRowsBytes {
		return ipeers.HostResult{
			Alias:                h.Alias,
			HostID:               resultHostID,
			OK:                   false,
			Error:                fmt.Sprintf("peer inventory too large (%d bytes)", len(encoded)),
			Peers:                []ipeers.PeerRecord{},
			UnknownRegistryFiles: []string{},
		}
	}

	// env.Error is the remote peer's own reported error text — bound and
	// prefix it the same way verifyHost does for the add/put 502 body, so
	// an attacker-controlled remote cannot inflate or pollute this row.
	rowErr := env.Error
	if rowErr != "" {
		rowErr = "peer: " + bound(rowErr)
	} else if !env.OK {
		// A peer that says ok=false and nothing else still gets a named
		// cause: this row is what the verify route (hosts_verify.go) and
		// the page render, and {ok:false, error:""} would be a red row
		// with no reason. Same string verifyHost uses for the 502 case.
		rowErr = "peer reported ok=false"
	}

	// unknown is the remote's own reported list of alive-but-undecodable
	// registry files (attacker-controlled, like Error): bounded to at most
	// 32 entries, each individually truncated the same way env.Error is,
	// so a misbehaving peer cannot inflate this row with an unbounded list
	// of long paths.
	unknown := env.UnknownRegistryFiles
	if len(unknown) > 32 {
		unknown = unknown[:32]
	}
	bounded := make([]string, len(unknown))
	for i, u := range unknown {
		bounded[i] = bound(u)
	}

	// env.DaemonVersion is the remote's own reported text, exactly as
	// attacker-controlled as env.Error and the unknown-registry-files list
	// above, so it is bounded the same way before this row is ever printed
	// or re-encoded. env.TitlesUnavailable is a bool and needs no bounding:
	// it is the remote's own claim about its label store, copied through
	// for the per-host cause line.
	//
	// env.Alias gets that same bounding and nothing more. It is NOT put
	// through sanitizeLearnedAlias: that helper answers "may we adopt this
	// name?" and returns "" when the answer is no, which would erase exactly
	// the reports worth seeing — a peer that renamed itself to something
	// unroutable, or to our own local alias. This field is only ever
	// displayed (sanitizeCell at the terminal), never stored and never
	// routed on, so the bar it clears is the one env.Error clears.
	return ipeers.HostResult{
		Alias:                h.Alias,
		SelfAlias:            bound(env.Alias),
		HostID:               bound(resultHostID),
		OK:                   env.OK,
		Error:                rowErr,
		Partial:              env.Partial,
		Peers:                peers,
		DaemonVersion:        bound(env.DaemonVersion),
		UnknownRegistryFiles: bounded,
		TitlesUnavailable:    env.TitlesUnavailable,
	}
}
