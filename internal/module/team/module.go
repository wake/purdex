package teammod

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/hostconfig"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// OriginResolver attributes a request's caller to a live CC session and
// answers whether a session is still live. The peers module's
// *OriginResolver is the production value (registry key
// peersmod.OriginResolverKey); tests inject a fake.
//
// ResolveOrigin's error is a registry read failure only: the caller answers
// it with 503 not_ready (retry), never origin_unknown. ok=false with a nil
// error means the registry was read and the inbox is not a live session.
type OriginResolver interface {
	ResolveOrigin(inbox string) (team.Origin, bool, error)
	// ResolveOriginBySession is ResolveOrigin keyed by CC session id: the
	// relay routes are called by the mod with its session id, not its
	// inbox (P5a). Same ok/err contract.
	ResolveOriginBySession(sessionID string) (team.Origin, bool, error)
	// ResolveOriginsBySession is the same for many sessions with one
	// registry read (the roster's build): a session the registry does not
	// list is absent from the map; the error is a registry read failure only.
	ResolveOriginsBySession(sessionIDs []string) (map[string]team.Origin, error)
	// ResolveOriginByRef is ResolveOriginBySession keyed by the conversation's CURRENT ref ("_xxxxxx"): `pdx adopt`
	// names its target by it. Same ok/err contract.
	ResolveOriginByRef(ref string) (team.Origin, bool, error)
	// InboxOf is the messaging socket of the session's live entry, for the notice outbox (PL-1d1).
	InboxOf(sessionID string) (inbox string, ok bool, err error)
	LiveSession(sessionID string) bool
	// ListLiveOrigins is every live, non-proxy session of this host (one registry read), for the unattended panel's
	// quota list. An error is a registry read failure only.
	ListLiveOrigins() ([]team.Origin, error)
	// SameProcess reports whether pid is alive and started at procStart (LeadPresence's step 2 alone): the
	// re-verification right before a signal is sent to an adopted member's process. A start time that cannot
	// be read, or a procStart that cannot be parsed, is an error — never "same".
	SameProcess(pid int, procStart string) (bool, error)
	// LeadPresence is a team lead's presence for the team end (spec §7.1),
	// which cannot be undone: tied to the lead's own process (pid and start
	// time as its request recorded them), PresenceGone only when that
	// process is dead, reused, or in another conversation.
	LeadPresence(sessionID string, pid int, procStart string) peersmod.Presence
}

// Module owns team.db and serves /api/team/*.
type Module struct {
	core    *core.Core
	store   *Store
	origins OriginResolver
	// responders answers "can anyone remote answer a hook_ask right now?"
	// (spec §6.6 step 1): the WS half (core.Events.HasSubscribers) in P8a;
	// the iOS line adds the push registry behind the same interface.
	responders RemoteResponders
	now        func() int64 // unix ms; injectable for tests
	logf       func(format string, args ...any)

	// P5a: the relay switches (host config), the title mover (meta.db; nil
	// without a meta store), the op/request id minter, the handoff
	// directory and what each session's mod said in hello (under mu).
	// modSeen is THE mod-presence record: P6 reads it for
	// relay_unsupported, P8a-1a's modPresent() reads it for the
	// terminal-only degradation; nothing else writes it.
	switches hostconfig.RelaySwitchReader
	// prompts is the relay prompt bodies (host config, spec §8.8), read
	// on every GET /api/relay/prompts.
	prompts hostconfig.RelayPromptReader
	// unattended is the U23 switch (host config), read under createMu by
	// every create and sweep; unattendedErr (under createMu) is the last
	// read error logged, so a corrupt value logs once, not every tick.
	unattended    hostconfig.UnattendedStore
	unattendedErr string
	quotaRuleErr  string // under createMu like unattendedErr: the last unreadable-switch error logged
	// notAutoApproved (under createMu) is, per open row the daemon could
	// not approve, the reason last logged (autoApprove): a refusal retried
	// every tick logs once. A sweep, and every tick while it is not empty,
	// forgets the rows no longer open. notAutoApprovedN is its size, which
	// the tick reads without createMu (rememberRefusal / forgetRefusal).
	notAutoApproved  map[string]string
	notAutoApprovedN atomic.Int64
	titles           TitleMover
	// usage is the agent module's per-session statusline reading; begin
	// copies model_id / effort from it into the self_relay payload (the mod
	// sends neither). Nil when the agent module is absent: both stay "".
	usage agent.ContextUsageReader
	// status is the agent module's status per tmux session and noticeAt the percentage the 70% idle notice fires at
	// (PDX_RELAY_THRESHOLD, read once at Init; P7-1).
	status   AgentStatusReader
	noticeAt int
	newID    func() string
	relayDir string
	modSeen  map[string]helloInfo

	// dataDir is the daemon's data dir; the hook lock flags live under
	// <dataDir>/hooklocks (spec §6.6): the hook decide route removes a flag
	// it answered {} for, and the sweeper prunes flags of sessions the
	// registry no longer lists.
	dataDir string

	// stopCtx is cancelled first in Stop: long-polls return, the sweeper
	// exits and POST create answers 503 not_ready. The DB stays open until
	// Close (PD6): in-flight handlers still read it during srv.Shutdown.
	stopCtx context.Context
	// cmdLimit is the per-lead-host admission of the cross-host commands route (spent before the body is decoded).
	cmdLimit   *peersmod.HostLimiter
	stopCancel context.CancelFunc
	sweepWG    sync.WaitGroup
	// noticeMu orders a late sweepWG.Add (handoverNoticeAsync) against Stop's cancel: the Add happens only while it is
	// held and stopping() is false, and Stop passes through it right after the cancel (a barrier), so no Add can follow the Wait.
	noticeMu sync.Mutex
	// beforeMemberRelayInsert, when set, runs in the member-relay create between its checks and the insert's transaction (tests race a release there).
	beforeMemberRelayInsert func(mr memberRow)
	// afterPoolSpend and afterMemberRowInsert fail the member-relay create's gate at that point (tests: fault injection).
	afterPoolSpend, afterMemberRowInsert func() error
	// helloMu orders a hello's modSeen update with its mod_hello write (P6-2a); never held with mu across the write.
	helloMu sync.Mutex
	// unsubTurnEnd ends the subscription to the agent module's turn ends (T-3a2); nil when none.
	unsubTurnEnd func()
	turnEndMu    sync.RWMutex // held (read) by a turn-end write in flight; Stop takes it once to wait for them
	tickN        int          // sweeper ticks so far; only the sweeper goroutine (or a test) touches it
	// bootAt is when Start ran (unix ms): no team ends before bootAt +
	// BootGraceS, the grace open requests get (spec §9.2). 0 for a module
	// that never started (most tests): no grace.
	bootAt int64
	// bootReconciled is set once the frame reconciliation has run after the boot grace; only the sweeper's goroutine (or a test) touches it.
	bootReconciled bool
	// listActiveOps replaces the store's ListActiveRelayOps for the after-grace reconciliation (tests: a failing list).
	listActiveOps func() ([]team.RelayOp, error)

	// createMu serialises create's check-then-insert (idempotent retry,
	// request_open, insert) and Stop's cancel of stopCtx: a create either
	// finishes before Stop, or takes the lock after it and sees stopping.
	createMu sync.Mutex

	mu      sync.Mutex
	waiters map[string][]chan struct{} // long-polls per approval id; closed when it closes

	// eventMu orders the approval.request stream (spec §6.2): it is held
	// across every opened/closed broadcast and across sendSnapshot's
	// ListOpen + send, so no event is queued between a snapshot's read and
	// its delivery. A client that replaces its set from the snapshot thus
	// never loses a just-opened request or revives a just-closed one. No
	// store write happens under it: each broadcast follows its own write.
	eventMu sync.Mutex
	// sessionSubs are the SubscribeSession subscriptions (approval_feed.go), under eventMu; responderHolds counts
	// the HoldResponder holders.
	sessionSubs    map[string]map[uint64]func(op string, a team.Approval) // by session id, then subscription id
	nextSessionSub uint64
	sessionSubN    int // subscriptions in all
	// approvalSubs are the SubscribeApprovals subscribers (approval_events.go), under eventMu.
	approvalSubs   map[uint64]*approvalSub
	nextApprovalID uint64
	approvalDrops  atomic.Int64
	responderHolds atomic.Int64

	// rosterMu orders the team.roster stream (plan PL-1f′): held across
	// rosterSync's read + send and across sendRosterSnapshot's, so a
	// snapshot and a changed never interleave. lastRosterHash is the hash
	// of the roster JSON last sent as changed, valid when rosterSent
	// (a snapshot that could not be built clears it). It is taken by the
	// publisher and by subscribe, never under createMu; no store write
	// happens under it.
	rosterMu       sync.Mutex
	lastRosterHash [sha256.Size]byte
	rosterSent     bool
	// rosterSig is the publisher's one-slot signal (rosterChanged);
	// rosterBarrier is the test seam runRoster serves (nothing in
	// production sends on it).
	rosterSig     chan struct{}
	rosterBarrier chan chan struct{}

	// afterRead, when set, runs in pollRow right after the row is read
	// and before the wait. Tests use it to close the row in that window
	// and prove the waiter was registered before the read; nil in production.
	afterRead func(id string)
	// afterSnapshotRead, when set, runs in sendSnapshot between its ListOpen
	// and its send; tests open or close a request in that window and prove
	// the event is delivered after the snapshot. nil in production.
	afterSnapshotRead func()
	// afterListOpen, when set, runs in tick between its ListOpen and its
	// closes; tests renew a lease in that window and prove the sweeper
	// does not close on the stale copy. nil in production.
	afterListOpen func()
	// afterOpenByOrigin, when set, runs in handleHookDecide between its
	// OpenByOrigin and its flag removal; tests run a create for the same
	// origin in that window and prove it waits for createMu. nil in
	// production.
	afterOpenByOrigin func()
	// afterOpenCheck, when set, runs in handleRelayBegin between its
	// OpenRelayOpBySession check and its CreateRelayOp; tests open an op
	// for the same session in that window and prove the table's conflict
	// (ErrRelayOpOpen) is answered as 409 relay_open too. nil in production.
	afterOpenCheck func(sessionID string)
	// afterOpenByToolUse, when set, runs in handleAskBegin between its
	// OpenByToolUse and its insert; tests start a second begin for the same
	// tool use in that window and prove it waits for createMu. nil in
	// production.
	afterOpenByToolUse func()
	// afterAskFlagQuery, when set, runs in refreshAskFlag between its read of
	// the session's open terminal_only rows and the flag write/removal; tests
	// open a row for the same session in that window and prove it waits for
	// createMu (else the stale read removes the new row's flag). nil in
	// production.
	afterAskFlagQuery func()
	// beforeTerminalClose is a test seam run by a terminal relay report just
	// before it closes the op's approval row (the approve that races it).
	beforeTerminalClose func(opID string)
	// beforeEndTeam, when set, runs in endGoneTeams after it decided the
	// team's lead is gone and just before EndTeam; tests move a relay op of
	// that lead in this window and prove the end loses. nil in production.
	beforeEndTeam func(t team.Team)
	// beforeMarkGone, when set, runs in markGoneMembers after it decided the
	// member is gone and just before MarkMemberGone; tests claim a relay of
	// that member there and prove the mark loses. nil in production.
	beforeMarkGone func(mr memberRow)
	// beforeKillMark, when set, runs in handleKill after the member's tmux
	// session was ended and just before its row is marked killed; tests move
	// or relay the member there and prove the mark loses. nil in production.
	beforeKillMark func(mr memberRow)
	// clearedWait / clearedPoll bound how long a cleared report waits for
	// the registry to show the new session id (checkClearedTarget).
	clearedWait, clearedPoll time.Duration

	// P4-5 spawn (spec §7.2, spawn_runner.go): the session create path, the
	// tmux executor (nil: spawn disabled), team.member_command, the agent
	// frames and the title store (nil: no title). spawnWG joins the runners
	// in Stop. spawnPoll and spawnSleep pace the registration poll,
	// spawnBudget (ms) bounds it.
	sessions    sessionCreator
	tmux        tmuxOps
	teamCfg     hostconfig.TeamSettingsReader
	frames      frameReader
	titleSet    TitleSetter
	spawnWG     sync.WaitGroup
	spawnPoll   time.Duration
	spawnSleep  func(ctx context.Context, d time.Duration)
	spawnBudget int64
	// spawnWait is how long POST /api/team/spawns waits for its op to leave
	// running (team.SpawnPollWaitS).
	spawnWait time.Duration
	// beforeSpawnStep, when set, runs before each runner step with the op as
	// read; tests hold or steer a runner there. afterSpawnTeamRead, when
	// set, runs in the spawn POST between its team read and the op's write;
	// tests end the team there and prove the write sees it. nil in production.
	beforeSpawnStep    func(op spawnRow)
	afterSpawnTeamRead func()
	// afterTaskLookup, when set, runs in the task routes right after the
	// handler found the task in the caller's scope and before the store call
	// that reads or writes it; tests change the world there and prove the
	// store checks the caller's right again. nil in production.
	afterTaskLookup func()
	// beforeCreateLock, when set, runs in handleCreate just before it takes
	// createMu; tests turn the unattended switch on there and prove the
	// create reads it under the lock. nil in production.
	beforeCreateLock func()
	// beforeAutoApprove, when set, runs in autoApprove before the approve;
	// an error fails that approve there (tests). nil in production.
	beforeAutoApprove func(a team.Approval) error
	// noticeKick is a test seam called after kickNotices when an adopt approval won; afterApproved is its
	// only caller. nil until then (tests count it).
	noticeKick func()
	// quotaRule reads the relay-quota rule's switch (hostconfig key relay_quota, #2062); nil → the rule is off. heldQuota
	// is the set of open self_relay rows the daemon could not approve because their chain's self_left is 0 (under heldMu,
	// written by the approve paths, read by the unattended GET without createMu).
	quotaRule hostconfig.RelayQuotaReader
	heldMu    sync.Mutex
	heldQuota map[string]struct{}
	spent     map[string]struct{} // approval ids whose transaction spent a unit, until afterApproved publishes (under heldMu)
	// quotaMu serialises a relay-quota PUT's commit, event and roster signal (quota_handler.go); afterQuotaSet is a test seam.
	quotaMu       sync.Mutex
	afterQuotaSet func()
	// killProcess signals an adopted member's Claude Code process (SIGTERM; tests inject).
	killProcess func(pid int) error
	// sender sends the notices (peers.SenderKey; nil → notices stay owed); noticeSig wakes the drain
	// (kickNotices); noticeLogAt is the drain's own record of when a row's failure was last logged.
	sender      peersmod.Sender
	noticeSig   chan struct{}
	noticeLogAt map[string]int64
	// beforeCloseExpired, when set, runs in closeExpired before the CAS;
	// an error fails that close there (tests). nil in production.
	beforeCloseExpired func(id string) error
}

// New returns a Module with production defaults.
func New() *Module {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	return &Module{
		now:        func() int64 { return time.Now().UnixMilli() },
		logf:       log.Printf,
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
		waiters:    map[string][]chan struct{}{},
		newID:      uuid.NewString,
		modSeen:    map[string]helloInfo{},
		// The roster publisher's signal (one slot) and its test barrier.
		rosterSig:     make(chan struct{}, 1),
		rosterBarrier: make(chan chan struct{}),
		noticeSig:     make(chan struct{}, 1),
		killProcess:   func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) },
		// A cleared report waits this long for the registry to show the new
		// session id (measured ~0.6 s after /clear), polling every 100 ms.
		clearedWait: 3 * time.Second,
		clearedPoll: 100 * time.Millisecond,
		spawnPoll:   250 * time.Millisecond,
		spawnSleep:  sleepCtx,
		spawnBudget: team.SpawnRegisterS * 1000,
		spawnWait:   team.SpawnPollWaitS * time.Second,
	}
}

// WithTitles sets the title mover (the meta store's PeerLabels in
// production). Nil is allowed: titles then stay on the old session id.
func (m *Module) WithTitles(t TitleMover) *Module {
	m.titles = t
	return m
}

func (m *Module) Name() string           { return "team" }
func (m *Module) Dependencies() []string { return []string{"agent", "peers", "hostconfig", "session"} }

// Init resolves the origin resolver peers registered and opens team.db in
// the data dir. Both are hard errors: without either the module cannot
// attribute or persist a single request.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	m.cmdLimit = newCommandLimiter()
	svc, ok := c.Registry.Get(peersmod.OriginResolverKey)
	if !ok {
		return fmt.Errorf("team: service %q not registered", peersmod.OriginResolverKey)
	}
	origins, ok := svc.(OriginResolver)
	if !ok {
		return fmt.Errorf("team: service %q does not implement OriginResolver (%T)", peersmod.OriginResolverKey, svc)
	}
	m.origins = origins
	if svc, ok := c.Registry.Get(peersmod.SenderKey); ok {
		if snd, ok := svc.(peersmod.Sender); ok {
			m.sender = snd
		}
	}
	sw, ok := c.Registry.Get(hostconfig.RelaySwitchesKey)
	if !ok {
		return fmt.Errorf("team: service %q not registered", hostconfig.RelaySwitchesKey)
	}
	switches, ok := sw.(hostconfig.RelaySwitchReader)
	if !ok {
		return fmt.Errorf("team: service %q does not implement RelaySwitchReader (%T)", hostconfig.RelaySwitchesKey, sw)
	}
	m.switches = switches
	prompts, err := lookup[hostconfig.RelayPromptReader](c, hostconfig.RelayPromptsKey)
	if err != nil {
		return err
	}
	m.prompts = prompts
	if m.unattended, err = lookup[hostconfig.UnattendedStore](c, hostconfig.UnattendedKey); err != nil {
		return err
	}
	if svc, ok := c.Registry.Get(hostconfig.RelayQuotaKey); ok { // optional: without it the rule is off
		if r, ok := svc.(hostconfig.RelayQuotaReader); ok {
			m.quotaRule = r
		}
	}
	if err := m.initSpawn(c); err != nil {
		return err
	}
	// The statusline reading lives in the agent module (P1); as peers does,
	// type-assert the reader on the owner-resolver service rather than add
	// a registry key. Optional: a daemon without it fills no model/effort.
	if svc, ok := c.Registry.Get(agent.OwnerResolverKey); ok {
		if r, ok := svc.(agent.ContextUsageReader); ok {
			m.usage = r
		}
		if r, ok := svc.(AgentStatusReader); ok {
			m.status = r
		}
	}
	m.noticeAt = noticeThreshold()
	store, err := OpenStore(filepath.Join(c.Cfg.DataDir, "team.db"))
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	m.store = store
	store.opChanged = m.wake                    // the one choke point: every committed change of an op wakes its long-polls
	seen, err := store.LoadModHello(modSeenCap) // presence outlives a restart (P6-2a)
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	m.mu.Lock()
	for sid, h := range seen {
		m.modSeen[sid] = h
	}
	m.mu.Unlock()
	if m.responders == nil {
		m.responders = wsResponders{events: c.Events}
	}
	m.dataDir = c.Cfg.DataDir
	m.relayDir = filepath.Join(c.Cfg.DataDir, team.RelayDir)
	// The peers inventory reads the relay lineage through this (spec §8.4).
	c.Registry.Register(team.LineageReaderKey, store)
	c.Registry.Register(team.ApprovalFeedKey, team.ApprovalFeed(m))
	c.Registry.Register(team.ApprovalEventsKey, team.ApprovalEvents(m))
	return nil
}

// RegisterRoutes mounts the /api/team/* routes, the hook decision route,
// the relay routes and the 分流 routes (Go method patterns).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+CommandsRoute, m.handleTeamCommand) // cross-host team commands (X2b); a host principal's, not the admin's
	mux.HandleFunc("POST /api/team/approvals", m.handleCreate)
	mux.HandleFunc("GET /api/team/approvals", m.handleList)
	mux.HandleFunc("GET /api/team/approvals/{id}", m.handleGet)
	mux.HandleFunc("DELETE /api/team/approvals/{id}", m.handleDelete)
	mux.HandleFunc("POST /api/team/approvals/{id}/decide", m.handleDecide)
	mux.HandleFunc("GET /api/team/inflight", m.handleInflight)
	mux.HandleFunc("POST /api/team/spawns", m.handleSpawn) // P4-5, spec §7.2
	mux.HandleFunc("GET /api/team", m.handleTeam)          // P4-6, spec §7.3
	mux.HandleFunc("GET "+RosterRoute, m.handleRosterGet)  // PL-1f′: every live team (D-U24-5)
	mux.HandleFunc("POST /api/team/kill", m.handleKill)
	mux.HandleFunc("POST /api/team/relays", m.handleRelayCreate)
	mux.HandleFunc("POST /api/team/release", m.handleRelease)
	// T-1b1: tasks (plan "Routes"); a lead sees its team's, a member its own.
	mux.HandleFunc("POST /api/team/tasks", m.handleTaskCreate)
	mux.HandleFunc("GET /api/team/tasks", m.handleTaskList)
	mux.HandleFunc("GET /api/team/tasks/{id}", m.handleTaskGet)
	mux.HandleFunc("POST /api/team/tasks/{id}/status", m.handleTaskStatus)
	mux.HandleFunc("POST /api/team/tasks/{id}/reassign", m.handleTaskReassign)
	// T-1b2: reports; a member reports on its own task, a lead or the owner reads.
	mux.HandleFunc("POST /api/team/reports", m.handleReportCreate)
	mux.HandleFunc("GET /api/team/reports", m.handleReportList)
	// U23: the unattended switch (unattended spec D-U23-1, D-U23-6), the App's.
	mux.HandleFunc("GET "+UnattendedRoute, m.handleUnattendedGet)
	mux.HandleFunc("PUT "+UnattendedRoute, m.handleUnattendedPut)
	mux.HandleFunc("PUT "+team.RelayQuotaRoute, m.handleRelayQuotaPut)
	mux.HandleFunc("PUT "+team.MaxMembersRoute, m.handleMaxMembersPut)
	mux.HandleFunc("POST /api/hooks/decide", m.handleHookDecide)
	// P5a relay routes (spec §8.3, §8.7); all under TokenAuth like /api/team/*.
	mux.HandleFunc("POST /api/relay/hello", m.handleRelayHello)
	mux.HandleFunc("POST /api/relay/begin", m.handleRelayBegin)
	mux.HandleFunc("GET /api/relay/wait/{id}", m.handleRelayWait)
	mux.HandleFunc("POST /api/relay/self", m.handleRelaySelf)
	mux.HandleFunc("POST /api/relay/ops/{id}/report", m.handleRelayReport)
	mux.HandleFunc("GET /api/relay/ops/{id}", m.handleRelayOp)
	mux.HandleFunc("POST /api/relay/ops/{id}/claim", m.handleRelayClaim)
	mux.HandleFunc("POST /api/relay/ops/{id}/seen", m.handleRelaySeen)
	mux.HandleFunc("POST /api/relay/compacted", m.handleRelayCompacted) // P7-2
	mux.HandleFunc("GET /api/relay/prompts", m.handleRelayPrompts)      // P9a, spec §8.8
	// P8a 分流 routes (spec §6.6); TokenAuth like /api/team/*.
	mux.HandleFunc("POST /api/ask/begin", m.handleAskBegin)
	mux.HandleFunc("GET /api/ask/wait/{id}", m.handleAskWait)
	mux.HandleFunc("POST /api/ask/report/{id}", m.handleAskReport)
}

// Start applies the boot lease grace (spec §9.2: every open request's
// lease becomes max(lease_until, boot + 30 s), so its pdx can reconnect;
// teams get the same 30 s before an absent lead ends one, bootAt),
// registers the snapshot for new subscribers and starts the sweeper. It
// does not prune hook lock flags: during that same grace a CC session may
// not have re-registered, so a registry snapshot taken here would call it
// dead and the prune would delete the flag of an open lead request. The
// sweeper prunes on its 10th tick, and never a flag whose request is open.
func (m *Module) Start(context.Context) error {
	m.bootAt = m.now() // before the sweeper starts: endGoneTeams reads it
	// <data_dir>/relay/ exists from boot (spec §8.3); begin re-creates it
	// too. A failure is logged, not fatal: begin reports its own.
	if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
		m.logf("[team] relay dir %s: %v", m.relayDir, err)
	}
	n, err := m.store.ExtendOpenLeases(m.now() + team.BootGraceS*1000)
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	if n > 0 {
		m.logf("[team] boot: extended the lease of %d open approval request(s) by %ds", n, team.BootGraceS)
	}
	m.rosterBaseline() // before the boot's own writes: each of them announces itself
	m.reconcileRelays()
	m.resumeSpawns()
	// U23 rule 7: requests left open across a restart while the switch is
	// on are approved now, not at the first tick; createMu as every reader
	// of the switch.
	m.createMu.Lock()
	if m.unattendedOn() {
		m.sweepUnattended("boot")
	}
	m.createMu.Unlock()
	if svc, ok := m.core.Registry.Get(agent.TerminalSessionsKey); ok {
		m.subscribeTurnEnd(svc) // after every Init: the agent module's service is there
	}
	m.core.Events.OnSubscribe(m.sendSnapshot)
	m.core.Events.OnSubscribe(m.sendUnattendedSnapshot)
	m.core.Events.OnSubscribe(m.sendRosterSnapshot)
	m.sweepWG.Add(4)
	go m.runNotices() // first: a notice owed across the restart goes out before the sweeper's first tick
	go m.runSweeper()
	go m.runRetention()
	go m.runRoster() // after the boot's own writes signalled: it publishes what they left
	m.logf("[team] endpoints enabled")
	return nil
}

// Stop cancels stopCtx (long-polls return, create answers not_ready) and
// joins the sweeper and the spawn runners, which leave their ops running
// at the recorded step for the next boot. Idempotent. The DB is closed in
// Close. The cancel is taken under createMu so no create inserts after
// Stop returns: one that is past its entry check waits for the lock and
// then re-checks stopping.
func (m *Module) Stop(context.Context) error {
	m.createMu.Lock()
	m.stopCancel()
	m.createMu.Unlock()
	m.noticeMu.Lock() // an Add that passed its check finishes before the Wait below; later ones see stopping()
	m.noticeMu.Unlock()
	if m.unsubTurnEnd != nil { // before the sweepers join: no turn end writes once Stop has begun
		m.unsubTurnEnd()
		m.unsubTurnEnd = nil
	}
	m.turnEndMu.Lock() // a turn-end write already past its stopping() check finishes first; later ones see stopping()
	m.turnEndMu.Unlock()
	m.dropSessionSubs()
	m.dropApprovalSubs()
	m.sweepWG.Wait()
	m.spawnWG.Wait()
	return nil
}

// Close closes team.db, after the HTTP server has stopped (core.Closer).
func (m *Module) Close() error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}

func (m *Module) stopping() bool {
	select {
	case <-m.stopCtx.Done():
		return true
	default:
		return false
	}
}

func (m *Module) hostID() string {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.HostID
}

// closeAs is the one close path: the store's CAS, then — for the winner
// only — the closed broadcast and the long-poll wake-up. So every close
// produces exactly one closed event, whoever raced for it (spec §6.2).
func (m *Module) closeAs(id string, c Close) (team.Approval, bool, error) {
	return m.closeWith(id, func() (team.Approval, bool, error) { return m.store.CloseIfOpen(id, c) })
}

// closeAsWithOp is closeAs for a close that a relay REPORT drives (a
// terminal report on an op still awaiting approval): the op takes the
// report's own state and reason instead of the mapping a close implies.
func (m *Module) closeAsWithOp(id string, c Close, rep RelayReport) (team.Approval, bool, error) {
	return m.closeWithOp(id, func() (team.Approval, bool, error) { return m.store.CloseIfOpen(id, c) }, &rep)
}

// closeWith is closeAs over a given store CAS — CloseIfOpen for decide,
// DELETE and a vanished origin; CloseIfExpired for the sweeper's timeout
// and lease paths. The winner alone broadcasts and wakes.
func (m *Module) closeWith(id string, cas func() (team.Approval, bool, error)) (team.Approval, bool, error) {
	return m.closeWithOp(id, cas, nil)
}

// closeWithOp is closeWith with the op report the winner applies to a
// self_relay row's op (nil: the mapping the row's state implies).
func (m *Module) closeWithOp(id string, cas func() (team.Approval, bool, error), rep *RelayReport) (team.Approval, bool, error) {
	after, won, err := cas()
	if err != nil {
		return team.Approval{}, false, err
	}
	if won {
		m.announceClosed(after, rep)
	}
	return after, won, nil
}

// announceClosed is what follows every close that won, once it is
// committed: the closed broadcast, the long-poll wake-up, afterClose and,
// for an approval, afterApproved — on every path, so the side effects of
// an approval live in one place.
func (m *Module) announceClosed(after team.Approval, rep *RelayReport) {
	m.broadcast("closed", &after)
	m.wake(after.ID)
	m.unhold(after.ID) // closed, however: no longer waiting for quota
	m.afterClose(after, rep)
	if after.State == team.StateApproved {
		m.afterApproved(after)
	}
}

// broadcast queues one opened/closed event to every subscriber, under
// eventMu so it cannot land between a snapshot's read and its send.
//
// It is strict for every subscriber (BroadcastStrict, #1970): one whose send
// buffer is full is removed and its client reconnects for the approval
// snapshot, instead of losing the frame and keeping its connection. A
// dropped opened would leave that window without the dialog, a dropped
// closed would leave the dialog up, and nothing would ever tell it. Approval
// events are rare, so the occasional reconnect is cheap.
func (m *Module) broadcast(op string, a *team.Approval) {
	v, err := json.Marshal(team.EventValue{Op: op, Approval: a})
	if err != nil {
		m.logf("[team] encode %s event: %v", op, err)
		return
	}
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	m.core.Events.BroadcastStrict(core.HostEvent{Type: team.EventType, Value: string(v)})
	if a != nil {
		m.deliverToSessionSubs(op, a)
		m.publishApprovalEvent(op, a)
	}
}

// sendSnapshot queues {op:"snapshot", approvals:[…]} to a new subscriber
// (spec §6.2: late or reconnecting clients see the same open set). The
// read and the send happen under eventMu, so every event the subscriber
// receives afterwards is for a change the snapshot does not yet show. A
// subscriber that did not get the snapshot — the open set could not be
// read, or its buffer is already full — is closed so it reconnects and
// asks again (as session/module.go does); keeping it would leave a client
// that never sees the requests open before it connected. One already
// removed is left alone.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	why := m.snapshotUnderLock(sub)
	if why == "" {
		return
	}
	select {
	case <-sub.Done():
	default:
		m.logf("[team] OnSubscribe snapshot %s; closing the connection so the client reconnects", why)
		m.core.Events.Remove(sub)
	}
}

// snapshotUnderLock reads the open set and queues it to sub, holding
// eventMu from the read to the send. It returns "" when the snapshot was
// queued, else why it was not (the open set could not be read; the send
// buffer is full or the subscriber is gone), for the caller to close the
// subscriber. An encode error is logged and reported as "", since
// reconnecting would not change it.
func (m *Module) snapshotUnderLock(sub *core.EventSubscriber) string {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	open, err := m.store.ListOpen()
	if err != nil {
		return fmt.Sprintf("could not read the open set (%v)", err)
	}
	if m.afterSnapshotRead != nil {
		m.afterSnapshotRead()
	}
	v, err := json.Marshal(team.EventValue{Op: "snapshot", Approvals: open})
	if err != nil {
		m.logf("[team] encode snapshot: %v", err)
		return ""
	}
	data, err := json.Marshal(core.HostEvent{Type: team.EventType, Value: string(v)})
	if err != nil {
		m.logf("[team] encode snapshot event: %v", err)
		return ""
	}
	if !sub.TrySend(data) {
		return "could not be queued (send buffer full)"
	}
	return ""
}

// addWaiter registers a long-poll on id; the channel is closed by wake.
func (m *Module) addWaiter(id string) chan struct{} {
	ch := make(chan struct{})
	m.mu.Lock()
	m.waiters[id] = append(m.waiters[id], ch)
	m.mu.Unlock()
	return ch
}

// removeWaiter drops one long-poll's channel; a no-op once wake took it.
func (m *Module) removeWaiter(id string, ch chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.waiters[id]
	for i, w := range ws {
		if w == ch {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(ws) == 0 {
		delete(m.waiters, id)
	} else {
		m.waiters[id] = ws
	}
}

// wake releases every long-poll on id.
func (m *Module) wake(id string) {
	m.mu.Lock()
	ws := m.waiters[id]
	delete(m.waiters, id)
	m.mu.Unlock()
	for _, ch := range ws {
		close(ch)
	}
}
