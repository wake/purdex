// Package peers implements the "peers" daemon module: GET /api/peers, a
// local inventory of this host's tmux sessions joined with the agent module's
// owner resolution and the Claude Code session registry; the peer-host
// management and settings routes; POST /api/peers/send, the outbound half
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

	// labels is the peer_labels store (Task 3/7): Snapshot joins into every
	// inventory build (localEnvelope); claim/release handlers land in Task
	// 7. labelMu guards those forthcoming write operations — Snapshot
	// itself is a plain DB read and needs no lock.
	labels  LabelStore
	labelMu sync.Mutex

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
}

// New constructs a peers Module with production defaults over audit (the
// meta store's PeerMessages; nil disables delivery with audit_unavailable)
// and labels (the meta store's PeerLabels; nil means every conversation has
// its default label and claims fail with store_unavailable). Collaborators
// (sessions, owners) and the helper manager are wired in Init: the manager
// needs the config's data dir and the daemon's own executable path, neither
// of which belongs in a constructor.
func New(audit AuditStore, labels LabelStore) *Module {
	registryDir := filepath.Join(".claude", "sessions")
	if home, err := os.UserHomeDir(); err == nil {
		registryDir = filepath.Join(home, ".claude", "sessions")
	}
	stopCtx, stopCancel := context.WithCancel(context.Background())
	m := &Module{
		registryDir:      registryDir,
		liveness:         ipeers.DefaultLiveness(),
		budget:           2 * time.Second,
		now:              time.Now, // the one clock seam; every other clock reader below takes m.now
		client:           newRemoteClient(),
		fetch:            fetchRemote,
		logf:             log.Printf,
		audit:            audit,
		labels:           labels,
		writeFrame:       ccuds.WriteFrame,
		sockWriteTimeout: ipeers.SocketWriteTimeout,
		newMsgID:         uuid.NewString,
		post:             postDeliver,
		stopCtx:          stopCtx,
		stopCancel:       stopCancel,
		replySem:         make(chan struct{}, replyWorkerCap),
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
	mux.HandleFunc("GET /api/peers/settings", m.handleGetSettings)
	mux.HandleFunc("PUT /api/peers/settings", m.handlePutSettings)
	mux.HandleFunc("POST /api/peers/send", m.handleSend)
	mux.HandleFunc("POST /api/peers/deliver", m.handleDeliver)
	mux.HandleFunc("GET /api/peers/log", m.handlePeersLog)
}

// handlePeers serves GET /api/peers. scope unset/"local" returns this
// host's local inventory only; scope=all fans out to every configured peer
// host in parallel (admin principal only — see policy.go's
// HostRoutePolicy, enforced again here in depth); any other scope is 400.
func (m *Module) handlePeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

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

// localEnvelope builds this host's own inventory from a caller-supplied
// hostID/alias: the response body for scope unset/"local", and the local
// row's peers/ok/partial/error for scope=all. It never touches CfgMu itself
// — the caller (handlePeers) takes the one config snapshot for the whole
// request, so the local row's labels and the host/host_id embedded in its
// own Peers records are always built from the same values.
func (m *Module) localEnvelope(ctx context.Context, hostID, alias string) ipeers.Envelope {
	deadline := m.now().Add(m.budget)

	writeError := func(errMsg string) ipeers.Envelope {
		return ipeers.Envelope{
			HostID:               hostID,
			OK:                   false,
			Error:                errMsg,
			Partial:              false,
			Peers:                []ipeers.PeerRecord{},
			DaemonVersion:        buildinfo.Version,
			UnknownRegistryFiles: []string{},
		}
	}

	instance := m.sessions.TmuxInstance()

	sessions, err := m.sessions.ListSessions()
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

		owner, ok, err := m.owners.ResolveSessionOwner(ctx, s.Code)
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
	if after := m.sessions.TmuxInstance(); instance != "" && after != "" && after != instance {
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

	// The label snapshot (Task 3's peer_labels table) is joined the same
	// way: a nil store or a read failure never blocks the inventory build
	// (every row still gets its default label), but a failed read is
	// reported the same way a failed owner lookup is — this response may
	// be showing stale/default labels it cannot vouch for.
	labels, labelsErr := m.labelSnapshot()
	if labelsErr != nil {
		m.logf("peers: inventory: label store unavailable, reporting default labels: %v", labelsErr)
	}

	partial := len(unresolved) > 0 || len(unknown) > 0 || labelsErr != nil

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
		Labels:     labels,
	})

	return ipeers.Envelope{
		HostID:               hostID,
		OK:                   true,
		Partial:              partial,
		Peers:                peerRecords,
		DaemonVersion:        buildinfo.Version,
		UnknownRegistryFiles: unknown,
	}
}

// labelSnapshot reads the label table into Build's map. A nil store is an
// empty map and no error (Peer Address v2 has never been configured with a
// label store, which is not this inventory's trouble); a read error is
// returned so the caller can mark the response partial — the rows it
// renders below fall back to their default label, same as an absent row.
func (m *Module) labelSnapshot() (map[string]ipeers.LabelInfo, error) {
	out := map[string]ipeers.LabelInfo{}
	if m.labels == nil {
		return out, nil
	}
	rows, err := m.labels.Snapshot()
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out[r.SessionID] = ipeers.LabelInfo{Label: r.Label, Rev: r.Rev}
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
		Alias:                alias,
		HostID:               hostID,
		OK:                   local.OK,
		Error:                local.Error,
		Partial:              local.Partial,
		Peers:                local.Peers,
		DaemonVersion:        local.DaemonVersion,
		UnknownRegistryFiles: local.UnknownRegistryFiles,
	}

	wg.Wait()

	return ipeers.AllEnvelope{Hosts: results}
}

// normalizeRemoteRows rewrites every row of a remote host's fan-out
// response into this host's local view: Host becomes alias (how WE have
// the peer configured, never the remote's own self-reported value), and
// HostID becomes hostID (the caller's already-verified/bounded value for
// this host). Address is rebuilt as "<alias>/<session>", where <session>
// is everything after the remote's own first "/" (a "cc:" address's colon
// survives intact); a remote address with no "/" at all (malformed) keeps
// the whole original address as the session part instead. If the rebuilt
// address still doesn't parse as a valid "<host>/<session>" pair
// (ipeers.SplitAddress) — e.g. an empty session part, or a session part
// that itself contains another "/" — Address is blanked rather than left
// as an unusable value; Host and HostID stay set. Exported at the package
// level (not a method) so P3 can reuse it as-is.
func normalizeRemoteRows(rows []ipeers.PeerRecord, alias, hostID string) []ipeers.PeerRecord {
	out := make([]ipeers.PeerRecord, len(rows))
	for i, rec := range rows {
		rec.Host = alias
		rec.HostID = hostID

		session := rec.Address
		if idx := strings.IndexByte(rec.Address, '/'); idx >= 0 {
			session = rec.Address[idx+1:]
		}
		rec.Address = alias + "/" + session

		if _, _, ok := ipeers.SplitAddress(rec.Address); !ok {
			rec.Address = ""
		}

		out[i] = rec
	}
	return out
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

	fetchCtx, cancel := context.WithTimeout(ctx, remoteFetchTimeout)
	defer cancel()

	env, err := m.fetch(fetchCtx, m.client, h.URL, h.Token)
	if err != nil {
		return ipeers.HostResult{
			Alias:                h.Alias,
			HostID:               h.HostID,
			OK:                   false,
			Error:                err.Error(),
			Peers:                []ipeers.PeerRecord{},
			UnknownRegistryFiles: []string{},
		}
	}

	if h.HostID != "" && env.HostID != h.HostID {
		return ipeers.HostResult{
			Alias:                h.Alias,
			HostID:               h.HostID,
			OK:                   false,
			Error:                fmt.Sprintf("host_id mismatch: got %s", boundRemoteText(env.HostID)),
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
		rowErr = "peer: " + boundRemoteText(rowErr)
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
		bounded[i] = boundRemoteText(u)
	}

	return ipeers.HostResult{
		Alias:                h.Alias,
		HostID:               resultHostID,
		OK:                   env.OK,
		Error:                rowErr,
		Partial:              env.Partial,
		Peers:                peers,
		DaemonVersion:        env.DaemonVersion,
		UnknownRegistryFiles: bounded,
	}
}
