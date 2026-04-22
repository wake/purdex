# PR-1b-1a — Observation 型別 + AGENT_ARB_MODE flag + trace_id strategy

> Phase：1（Schema + 雙寫過渡）
> 依賴：PR-1b-0 (#564, alpha.202)
> 後續：PR-1b-1b（Arbitrator goroutine + admission + apply pipeline）
> Spec 對照：§3.3（Observation）、§3.5 line 482-508（trace_id）、§8.3（AGENT_ARB_MODE）
> 關聯 Issue：#568 trace_id per-session-per-generation strategy

## Context

PR-1b-0 已把 §3.5 envelope 11 個新欄位 + row class discriminator 鋪到 SQLite。hook path 目前用 `trace_id == chain_id` 做 transitional aliasing（每次 hook callback 新建一個 chain，trace_id 跟著複製），**不符合 spec §3.5 line 483** 「per session per generation」語意；defender review 在 #568 點名此缺口。

PR-1b-1 原本打算一次交付 Observation 型別 + Arbitrator + 雙寫 passthrough，合計 2000-3000 LOC。累計 PR-1a + PR-1b-0 已 8 commits / 9 輪 review，reviewer 疲勞。拆成 a/b/c 三段後，**PR-1b-1a 只落資料模型與開關**，不含執行邏輯，約 600 LOC。

**關於 #568 — 分階段交付**：此 PR 落地 trace_id 的**型別契約 + Registry primitive**；實際 Mint trigger（SessionStart hook）在 PR-1b-1b，hook trace 路徑汰換（取代 chain_id aliasing）在 PR-1b-1c。Issue #568 直到 1b-1c merge 後才可關；本 PR 僅解 foundation。

## Goals

1. 定義 §3.3 Observation 型別與所有子型別（`SourceKind` / `ObsPhase` / `StateProposal` / `DecisionPort` / `Branch` / `EvidenceRef` / `ActorKey`）
2. Observation builder + validation（cap 16 DecisionPort / required fields / SourceKind enum / WatcherToken 規則）
3. 解決 #568：提供 per-(session, generation) trace_id 生成機制，hook path aliasing 暫時保留（由 1b-1c 接 Observation 路徑後汰換）
4. 導入 `AGENT_ARB_MODE` feature flag（env > config，env-locked 語意，預設 passthrough，切換鎖到下次 SessionStart）
5. `GET /api/agent/arbitrator/mode` 唯讀 API 回傳 `{current, pending, env_locked}`

## Non-Goals（明確排除）

- ❌ Arbitrator goroutine、channel admission、apply pipeline — **PR-1b-1b**
- ❌ Pending window、reconcile loop — **PR-1b-1b**
- ❌ hook/probe/sweep 轉 Observation 實際寫入 — **PR-1b-1c**
- ❌ Divergence 比對與 `frame_divergences` 寫入（表已在，但比對邏輯延後） — **PR-1b-1c**
- ❌ Monitor API envelope 穿透 + SPA type 同步（#569） — **PR-1b-1c**
- ❌ 現有 hook trace path 切換到新 trace_id（`trace.go` line 202-206 保留 chain_id aliasing 不動） — **PR-1b-1c**

## 設計決策

### D1. Package 組織

**決策**：新增兩個 package 放在 `internal/module/agent/` 下
- `internal/module/agent/observation/` — Observation 型別、builder、validation、TraceIDRegistry
- `internal/module/agent/arbmode/` — `AGENT_ARB_MODE` state machine + API handler

**理由**：
- 與既有 `internal/module/agent/{trace,sweep,verify,...}` 同層，符合現況 feature-module 組織
- 不放 `internal/agent/`（那是 provider layer, codex/cc/opencode/probe）
- `observation` 與 `arbmode` 分離：前者純資料模型 + 工具函式無 side-effect；後者牽涉 config 熱重載 + HTTP handler，職責不同

### D2. `ActorKey` 設計

**結構**（§3.2 / §3.3）：
```go
type ActorKey struct {
    SessionID  string  // tmux session id（與 frame 對齊）
    Generation int64   // frame.Generation；跨 generation 的 actor 視為不同 actor
    ActorID    string  // "primary:main" / "subagent:explore-42" / "proxy:editor"
}
```
- 提供 `String() string`（格式 `<sessionID>/<generation>/<actorID>`）供 log / idempotency hash 用
- Composite key 不用 tuple alias，因為 Go map 需要 `struct{}` 型別

### D3. Observation cap / validation 規則

| 檢查項 | 規則 | 失敗行為 |
|---|---|---|
| `len(DecisionPorts)` ≤ 16 | 超過視為程式錯誤 | dev: panic；prod: log warn + truncate 到 16（保留前 16 筆，棄後續）|
| `SourceKind` 值域 | `hook` / `probe` / `sweep` / `reconcile` / `synthetic` | 不合法 builder 回 `ErrInvalidSourceKind` |
| `Phase` 值域 | `proposed` / `committed` / `rejected`（spec §3.3）**必填** | 空值或非法 builder 回 `ErrInvalidPhase` |
| `WatcherToken` | `probe` source **必填**；其他 source 選填 | probe 缺 token builder 回 `ErrMissingWatcherToken` |
| 必填欄位 | `TraceID` / `SessionID` / `SourceKind` / `Action` / `Phase` / `ObservedAt` | 缺任一 builder 回 `ErrMissingRequiredField`（`ObservedGeneration` 為 int64 允許 zero-value，不強制；但 ActorKey.Generation mismatch 檢查仍需一致）|
| `Proposal.ActorKey.SessionID` | 必須 == `Observation.SessionID` | 不一致回 `ErrActorKeySessionMismatch` |
| `Proposal.ActorKey.Generation` | 必須 == `Observation.ObservedGeneration` | 不一致回 `ErrActorKeyGenerationMismatch`（D2 composite key 的 integrity 保證）|

**Dev / prod 判定**：統一走 `BuilderOption` 單一機制（D7）；不使用 build tag。預設 prod（log + truncate）；測試檔顯式 `NewBuilder(registry, WithDevMode(true))` 觸 panic 路徑。

### D4. #568 trace_id 策略 — **Minted at SessionStart**

選項評估：

| 選項 | 描述 | 取捨 |
|---|---|---|
| A. Deterministic (uuidv5) | `trace_id = uuidv5(NS_LIGHTS, session_id+generation)` | ✅ 無 state、可 reproduce；❌ daemon restart 前後同 trace_id，違反 §8 line 1222 「span IDs 不延續」 |
| **B. Minted at SessionStart** ✅ | SessionStart hook 觸發 `registry.Mint(session, generation)` → 塞 `map[Key]uuidv4`；Observation builder 呼叫 `registry.Get()` | ✅ 符合 spec 「restart 開新 trace_id」；❌ 需要 in-memory state |

**採 B**。實作：
```go
type TraceIDKey struct {
    SessionID  string
    Generation int64
}

type TraceIDRegistry struct {
    mu    sync.RWMutex
    cache map[TraceIDKey]string  // trace_id (uuidv4)
}

// Mint：若 key 已存在 → 回既有值 + log warn（同 generation 重複 mint 視為 bug，
// 但絕不 rotate，否則會把已發出觀察的 trace_id 切裂，破壞 #568 correlation）
func (r *TraceIDRegistry) Mint(sessionID string, generation int64) string

// Get：命中回 (id, true)；未命中回 ("", false)
func (r *TraceIDRegistry) Get(sessionID string, generation int64) (string, bool)

// PruneSessionBefore：清除指定 session 在 generation 之前的所有 trace_id
// 由 Arbitrator 在 SessionStart apply 完成後呼叫（新 generation 推進時舊的不再需要）
// 同時提供 PruneSession(sessionID) 清整個 session — SessionEnd observation 呼叫
func (r *TraceIDRegistry) PruneSessionBefore(sessionID string, generation int64) int
func (r *TraceIDRegistry) PruneSession(sessionID string) int

// 注意：**沒有 MintOrGet**。Miss 代表 SessionStart Mint 尚未觸發或 key 錯誤，
// 絕對不可在 Get path 自行 mint（避免 A → B 切裂）。
// Observation builder miss 時應回 error (ErrTraceIDNotMinted)，caller 決定是否重試。

// Daemon restart：registry 是 in-memory，重啟即清空 — 符合 spec §8 line 1222
```

**Prune 策略**（解決 P1 memory leak）：
- 每次 SessionStart apply 後 Arbitrator 呼叫 `PruneSessionBefore(sessionID, newGeneration)` 清舊 generation
- 若無法取得明確 SessionEnd 信號，alpha 階段可以「每 session 只保留最近 N=3 個 generation」（在 Mint 時順手 evict）；v1 前收緊策略
- 實際 prune trigger 在 PR-1b-1b（Arbitrator apply pipeline）；PR-1b-1a 只提供 API contract + 單元測試

**既有 hook path aliasing 保留**：`trace.go` line 202-206 `traceID = chain.ChainID` 不動；PR-1b-1c 接入 Observation 時才改。PR-1b-1a **不改 hook 實際路徑**，只提供 registry API。

**Mint 的呼叫者是誰？** — PR-1b-1a 不實際呼叫，但在 plan 內明示：1b-1b Arbitrator 在 apply SessionStart hook 觀察後呼叫 `Mint()` + `PruneSessionBefore()`；1b-1c hook path 改用 `Get()` 取 trace_id（miss 則 log error，**不自行 mint**）。

**UUID format**：`uuidv4`（`github.com/google/uuid` 已在 go.mod）。

### D5. AGENT_ARB_MODE 狀態機

**`ArbMode` type**：
```go
type ArbMode string
const (
    ModePassthrough   ArbMode = "passthrough"
    ModeAuthoritative ArbMode = "authoritative"
)
```

**Manager struct**：
```go
type Manager struct {
    mu          sync.RWMutex
    current     ArbMode   // 當前生效
    pending     ArbMode   // 下一 SessionStart 套用
    envLocked   bool      // env 鎖定 config hot reload
    envValue    ArbMode
}

type Snapshot struct {
    Current   ArbMode
    Pending   ArbMode
    EnvLocked bool
}

// env/configVal are raw strings (env var value / TOML config value); Manager
// validates internally. Invalid/empty values fall back to ModePassthrough with log warn.
func NewManager(env, configVal string) *Manager
// Snapshot：在單一 Lock 範圍內取三欄，避免 mid-config-change 讀到撕裂快照
func (m *Manager) Snapshot() Snapshot
func (m *Manager) OnConfigChange(configVal ArbMode) (changed bool)  // env-locked 時 no-op
func (m *Manager) ApplyAtSessionStart()  // pending → current
```

**API handler 一律透過 `Snapshot()` 取狀態**；不暴露分欄 getter（否則 handler 需要自行跨 call 處理一致性）。

**Boot 邏輯**：
1. 讀 `AGENT_ARB_MODE` env
2. 若 env set 且值合法 → `current = pending = env value, envLocked = true`
3. 若 env set 但值非法 → log warn + 視同 env unset
4. Env unset → `current = pending = config [agent] arb_mode`（缺省預設 `passthrough`）

**Hot reload 流程**：
- `core.OnConfigChange` callback 讀 config `[agent] arb_mode`
- `manager.OnConfigChange(configVal)`：env-locked log warn + 忽略；否則 `pending = configVal`（若跟 current 不同）
- **`current` 不立即切換**；等 `ApplyAtSessionStart()` 觸發（由 1b-1b Arbitrator 在 SessionStart 時呼叫）

**`agent.arb_mode` config 欄位**（路徑與 hot reload 機制對齊現況）：
- 加到 **`internal/config/config.go`** 的 `Config` struct：新增 `Agent AgentConfig`（或既有 struct 加欄位）
  - 專案的 config struct 在 `internal/config/`（不是 `internal/core/config.go`）
- TOML 預設值 `"passthrough"`；config_test.go 加 default 驗證
- 不合法值 decode 成功但 Manager init 時 fallback + log warn
- **`PUT /api/config` 是 JSON partial update**（非 TOML）— 在 `internal/core/config_handler.go:27` `configUpdateRequest` struct 加 `Agent *AgentUpdateRequest` 欄位，照既有 `Stream *config.StreamConfig` / `Terminal *config.TerminalConfig` 模式
- Hot reload：`handlePutConfig` 更新完呼叫 `c.NotifyConfigChange()`（現有機制，`internal/core/config_handler.go:121`）；Manager 透過 `core.OnConfigChange()` 訂閱後讀新值 → `OnConfigChange(newVal)` 更新 pending

### D6. API endpoint

`GET /api/agent/arbitrator/mode` 由 `arbmode.Handler` 註冊到 core mux 或 agent module mux：

Response：
```json
{
  "current":   "passthrough",
  "pending":   "authoritative",
  "env_locked": false
}
```

- 回傳 `application/json`；沒有任何寫入接口（POST/PUT 在 PR-1b-1b 或以後才考慮）
- 錯誤狀況無（純讀 Manager 狀態），一律 200

### D7. 關於 dev/prod 模式

**統一走 `BuilderOption`，不使用 build tag、不引入全域 env**。Observation builder 透過 constructor option：
```go
type BuilderOption func(*Builder)
func WithDevMode(dev bool) BuilderOption
```
- 測試檔顯式呼叫 `NewBuilder(registry, WithDevMode(true))`
- 生產碼 `NewBuilder(registry)` 預設 prod（log + truncate）
- 避免把 dev/prod 變成 global state
- Panic / log warn 行為由 Builder 本身決定，caller 無需知道當前模式

### D8. EvidenceRef shape

Spec §3.3 只列 `Evidence []EvidenceRef`，未單獨定義 struct；但 line 977-980 範例已明示 shape：
```go
Evidence: []EvidenceRef{
    {Key: "parent_actor_key", Value: parentActor.Key},
    {Key: "subagent_type",    Value: hookDetail["agent_type"]},
}
```

**決定 shape**（PR-1b-1a 定義，1b-1b/1c 不再變）：
```go
type EvidenceRef struct {
    Key   string  // low-cardinality label; e.g., "pid" / "parent_actor_key" / "screen_hash" / "hook_payload_ref"
    Value any     // string / int64 / ActorKey / map — JSON marshal 時由調用方保證可序列化
}
```

- `Value any` 因為 spec 例子已混用 string 與 ActorKey 等 struct
- JSON marshal 時 `Value` 欄位由 `encoding/json` 預設序列化；不可序列化的型別（chan / func）呼叫方責任
- **不**提供 typed variant `StringEvidenceRef` / `ActorKeyEvidenceRef` — 避免過早抽象；使用端若需要檢驗型別再上層 assert
- Used by `Observation.Evidence` / `DecisionPort.InputRefs`（spec line 247）

## Tasks（staged parallelism）

**執行順序**（有實際依賴）：

| Stage | 可並行 Task | 依賴 |
|---|---|---|
| **Stage 1** | Task 1（型別）, Task 4（Manager） | 無 |
| **Stage 2** | Task 2（Builder）, Task 3（TraceIDRegistry）, Task 5（API + wiring） | Task 2/3 依賴 Task 1 型別；Task 5 依賴 Task 4 Manager |

Controller（主 Claude）依此順序派發，不可無視依賴並行全部。每個 Task 完成後經 spec reviewer + code quality reviewer 雙審再開下一個。

### Task 1 — Observation 型別 + 子型別 + ActorKey + EvidenceRef

**檔案**：
- `internal/module/agent/observation/observation.go`（新增）
- `internal/module/agent/observation/observation_test.go`（新增）
- `internal/module/agent/observation/actor_key.go`（新增）
- `internal/module/agent/observation/actor_key_test.go`（新增）
- `internal/module/agent/observation/evidence.go`（新增，EvidenceRef 與相關 helpers）
- `internal/module/agent/observation/evidence_test.go`（新增）

**TDD checklist**（先寫測試）：
- [ ] `TestActorKey_String_Format` — `String()` 輸出 `<sessionID>/<generation>/<actorID>`
- [ ] `TestActorKey_Equal` — 相同 triple equal，任一欄位差異 not equal
- [ ] `TestActorKey_UsableAsMapKey` — `map[ActorKey]string` 寫入/讀取不 panic
- [ ] `TestSourceKind_ValidValues` — 5 個合法值 `IsValid()` 皆 true；未知值 false
- [ ] `TestObsPhase_ValidValues` — `proposed` / `committed` / `rejected` 合法，其他非法
- [ ] `TestObservation_ZeroValue_String` — `Observation{}.String()` 不 panic
- [ ] `TestStateProposal_ZeroValue` — roundtrip through JSON marshal/unmarshal 不 lossy
- [ ] `TestDecisionPort_ZeroValue_JSON` — `DecisionPort{}` marshal 後 unmarshal 回原值
- [ ] `TestDecisionPort_InputRefs_EvidenceRef_JSON` — `InputRefs []EvidenceRef` 含多筆 entry roundtrip 成功
- [ ] `TestBranch_Fields` — `ID` / `Condition` / `Outcome` 三欄 roundtrip
- [ ] `TestEvidenceRef_StringValue_JSON` — `{Key:"pid", Value:"12345"}` marshal/unmarshal roundtrip
- [ ] `TestEvidenceRef_Int64Value_JSON` — `{Key:"pid", Value:int64(12345)}` marshal；unmarshal 因 JSON number 會回 float64 → 文件化此行為，測試只驗 Key 一致 + Value 非空
- [ ] `TestEvidenceRef_ActorKeyValue_JSON` — `{Key:"parent_actor_key", Value:ActorKey{...}}` marshal 後含 `session_id/generation/actor_id` 三鍵
- [ ] `TestEvidenceRef_ZeroValue` — `EvidenceRef{}` marshal 回 `{"key":"","value":null}` 不 panic

**實作注意**：
- 型別全部在單一 package，不向外暴露 internal struct
- `Observation` struct 欄位順序依 spec §3.3 line 208-225
- `SourceKind.IsValid()` 用 switch case，不用 map（avoid alloc）
- `ActorKey.String()` 用 `fmt.Sprintf`，避開 `strings.Builder`（簡潔）
- `EvidenceRef.Value` 型別 `any`（`interface{}`）；JSON unmarshal 不做型別 assertion，caller 責任

**Subagent 交付標準**：
- 所有測試通過 (`go test ./internal/module/agent/observation/...`)
- `go vet` 通過
- 沒有跨 package 依賴（只進不出）

### Task 2 — Observation Builder + Validation

**檔案**：
- `internal/module/agent/observation/builder.go`（新增）
- `internal/module/agent/observation/builder_test.go`（新增）
- `internal/module/agent/observation/errors.go`（新增，定義 sentinel errors）

**TDD checklist**：
- [ ] `TestBuilder_Build_Minimal` — 填齊 required fields + empty DecisionPorts，`Build()` 成功
- [ ] `TestBuilder_MissingTraceID_Error` — 其他齊備缺 TraceID → `ErrMissingRequiredField`
- [ ] `TestBuilder_MissingSessionID_Error`
- [ ] `TestBuilder_MissingSourceKind_Error`
- [ ] `TestBuilder_MissingAction_Error`
- [ ] `TestBuilder_MissingPhase_Error` — Phase 為空 → `ErrMissingRequiredField`
- [ ] `TestBuilder_InvalidPhase_Error` — Phase="bogus" → `ErrInvalidPhase`
- [ ] `TestBuilder_ZeroObservedGeneration_OK` — ObservedGeneration=0 合法（int64 允許 zero-value；mismatch 檢查透過 ActorKey.Generation 比對）
- [ ] `TestBuilder_ZeroObservedAt_Error` — `time.Time{}` 觸發 error
- [ ] `TestBuilder_InvalidSourceKind_Error` — source="invalid" → `ErrInvalidSourceKind`
- [ ] `TestBuilder_ProbeWithoutWatcherToken_Error` — source=probe, WatcherToken="" → `ErrMissingWatcherToken`
- [ ] `TestBuilder_HookWithoutWatcherToken_OK` — source=hook, WatcherToken="" 正常
- [ ] `TestBuilder_ActorKeySessionMismatch_Error` — `Proposal.ActorKey.SessionID != Observation.SessionID` → `ErrActorKeySessionMismatch`
- [ ] `TestBuilder_ActorKeyGenerationMismatch_Error` — `Proposal.ActorKey.Generation != Observation.ObservedGeneration` → `ErrActorKeyGenerationMismatch`
- [ ] `TestBuilder_DecisionPorts_Exactly16_OK`
- [ ] `TestBuilder_DecisionPorts_17_ProdTruncate` — WithDevMode(false)；回 observation 含 16 筆，log 有 "decision_ports truncated" warn（1b-1a 暫 log；metrics counter 待 1b-1b 補）
- [ ] `TestBuilder_DecisionPorts_17_DevPanic` — WithDevMode(true)；`Build()` panic（recover 驗證 message）
- [ ] `TestBuilder_DecisionPorts_Empty_OK` — 允許 empty slice

**實作注意**：
- Sentinel error list 用 `errors.Is` 可識別
- Truncate 時記 metric：`observation.go` 定義 `lightsDecisionPortsTruncated prometheus.Counter`（或 expvar，看專案現況）
  - 若專案尚未有 metrics 基礎 → **用 log.Printf + TODO 註解，等 1b-1b 補 metrics**（不自建 metrics 子系統）
- `Build()` 接 `validate()` helper；validate fail 回 non-nil error，caller 決定是否 log

**Subagent 交付標準**：
- 所有測試通過含 panic case
- 沒有向 Task 1 之外的 package 加依賴
- `errors.go` 所有 error 有 godoc 註解說明觸發條件

### Task 3 — TraceIDRegistry (#568)

**檔案**：
- `internal/module/agent/observation/trace_id.go`（新增）
- `internal/module/agent/observation/trace_id_test.go`（新增）

**TDD checklist**：
- [ ] `TestTraceIDRegistry_Mint_NewKey_UUIDv4` — `Mint("s1", 1)` 回 uuidv4 格式字串
- [ ] `TestTraceIDRegistry_Mint_SameKeyTwice_ReturnsExisting` — 同 key 呼叫兩次 Mint → 回同一個 id（**不 rotate**）+ 第二次 log warn（"mint called twice"）。Rotate 會切裂 trace_id，破壞 #568 correlation
- [ ] `TestTraceIDRegistry_Get_Hit` — Mint 後 Get 回 same id + true
- [ ] `TestTraceIDRegistry_Get_Miss` — 未 Mint 直接 Get → "", false
- [ ] `TestTraceIDRegistry_DifferentGeneration_DifferentID` — `Mint("s1", 1)` 與 `Mint("s1", 2)` 分別產生不同 id（兩者皆先 mint 再 Get）
- [ ] `TestTraceIDRegistry_DifferentSession_DifferentID` — `Mint("s1", 1)` 與 `Mint("s2", 1)` 分別產生不同 id
- [ ] `TestTraceIDRegistry_PruneSessionBefore` — Mint (s1, 1), (s1, 2), (s1, 3) → `PruneSessionBefore("s1", 3)` 回 2；`Get("s1", 1)` / `Get("s1", 2)` 皆 miss；`Get("s1", 3)` hit
- [ ] `TestTraceIDRegistry_PruneSession` — `PruneSession("s1")` 清 s1 所有 entry；s2 entry 不動
- [ ] `TestTraceIDRegistry_PruneSessionBefore_NonExistent` — 呼叫無 entry 的 session 回 0，不 panic
- [ ] `TestTraceIDRegistry_Concurrent_Race` — 100 goroutine 並發 Mint 不同 key + 並發 Prune 無 race（`go test -race`）
- [ ] `TestTraceIDRegistry_FreshInstance_EmptyState` — `NewTraceIDRegistry()` 回傳 registry 所有 Get 皆 miss（模擬 daemon restart）

**實作注意**：
- UUID 來源 `github.com/google/uuid`
- `sync.RWMutex`：Get 用 RLock；Mint / Prune 用 Lock
- **無 `MintOrGet` / `Clear()` / `Delete()`**：Miss 時 caller 自行處理（log error + 回 error），不讓 registry 默默補 mint（會切裂）
- 不暴露 internal map；所有 mutation 通過 method

**Subagent 交付標準**：
- `go test -race` 通過
- UUID 格式驗證用 `uuid.Parse`（合法則測試通過）

### Task 4 — AGENT_ARB_MODE Manager

**檔案**：
- `internal/module/agent/arbmode/manager.go`（新增）
- `internal/module/agent/arbmode/manager_test.go`（新增）
- `internal/module/agent/arbmode/mode.go`（`ArbMode` type + const + `IsValid`）

**TDD checklist**：
- [ ] `TestArbMode_IsValid` — passthrough/authoritative 合法；其他非法
- [ ] `TestManager_DefaultPassthrough_Snapshot` — env="" config="" → `Snapshot() == {Current:passthrough, Pending:passthrough, EnvLocked:false}`
- [ ] `TestManager_ConfigOnly_Authoritative` — env="" config=authoritative → Snapshot.Current=authoritative
- [ ] `TestManager_EnvPassthrough_ConfigAuthoritative_EnvWins` — env=passthrough config=authoritative → Snapshot.Current=passthrough EnvLocked=true
- [ ] `TestManager_EnvInvalid_FallbackToConfig` — env="bogus" config=authoritative → Snapshot.Current=authoritative EnvLocked=false + log warn
- [ ] `TestManager_OnConfigChange_EnvUnset_UpdatesPending` — env unset，Snapshot.Current=passthrough；OnConfigChange(authoritative) → Snapshot.Pending=authoritative Snapshot.Current=passthrough returns changed=true
- [ ] `TestManager_OnConfigChange_EnvLocked_NoOp` — EnvLocked=true；OnConfigChange(authoritative) → Snapshot.Pending unchanged returns changed=false + log warn
- [ ] `TestManager_OnConfigChange_SameValue_NoOp` — 相同值 changed=false 無 log
- [ ] `TestManager_ApplyAtSessionStart_PromotesPending` — Snapshot.Current=passthrough Snapshot.Pending=authoritative → ApplyAtSessionStart() 後 Snapshot.Current=authoritative Snapshot.Pending=authoritative
- [ ] `TestManager_ApplyAtSessionStart_NoPendingDiff_NoOp` — current==pending；Apply no-op
- [ ] `TestManager_Snapshot_NotTornDuringConfigChange` — 一個 goroutine 反覆 OnConfigChange 交替 authoritative/passthrough；另一 goroutine 反覆 Snapshot；Snapshot 每次回傳必為 self-consistent（Current+Pending+EnvLocked 來自同一 lock 時段，非 partial mix）
- [ ] `TestManager_ConcurrentReadWrite_Race` — OnConfigChange 與 Snapshot 並發無 race（`go test -race`）

**實作注意**：
- `sync.RWMutex` 保護 current / pending / envLocked / envValue 四欄
- 所有 write path 經單一 Lock 範圍，避免部分更新
- `Snapshot()` 必須在同一 RLock 範圍內讀三欄，一次性回傳 Snapshot struct
- `log.Printf` 用 `[arbmode]` 前綴
- 不暴露分欄 `Current()` / `Pending()` / `EnvLocked()` getter（避免 caller 自行拼湊破壞一致性）

**Subagent 交付標準**：
- `go test -race` 通過
- 所有 log.Printf output 在測試時可被捕獲（`log.SetOutput(&buf)`）

### Task 5 — Arbitrator Mode API endpoint + Config wiring

**檔案**：
- `internal/module/agent/arbmode/handler.go`（新增）
- `internal/module/agent/arbmode/handler_test.go`（新增）
- **`internal/config/config.go`**（修改：加 `AgentConfig` struct 或對應欄位，含 `ArbMode string \`toml:"arb_mode"\``）
- **`internal/config/config_test.go`**（修改：驗證 TOML default + explicit value + invalid value 行為）
- **`internal/core/config_handler.go`**（修改：`configUpdateRequest` 加 `Agent *agentUpdateRequest` 欄位；handlePutConfig 多處理一段 Agent partial update）
- **`internal/core/config_handler_test.go`**（修改：PUT /api/config 含 agent.arb_mode 的測試）
- `internal/module/agent/module.go` 或 wiring 點（修改：建 Manager + 註冊 handler + 接 `core.OnConfigChange`）

**TDD checklist**：
- [ ] `TestAgentConfig_ArbMode_DefaultPassthrough` — 空 TOML decode 後 ArbMode = "passthrough"（或空字串 → Manager init fallback）
- [ ] `TestAgentConfig_ArbMode_ExplicitAuthoritative` — TOML `[agent]\narb_mode = "authoritative"` decode 成功
- [ ] `TestAgentConfig_ArbMode_InvalidValue_AcceptedByDecoder` — TOML 非法值 decode 成功（純字串）；後續 Manager init 時 fallback（log warn）
- [ ] `TestPutConfig_Agent_ArbMode_Passthrough` — PUT body `{"agent":{"arb_mode":"passthrough"}}` → 200 + in-memory Cfg.Agent.ArbMode 更新
- [ ] `TestPutConfig_Agent_ArbMode_InvalidValue_400` — PUT body `{"agent":{"arb_mode":"bogus"}}` → 400 "invalid arb_mode"
- [ ] `TestPutConfig_Agent_ArbMode_TriggersOnConfigChange` — PUT 成功後 OnConfigChange callback 被呼叫一次
- [ ] `TestPutConfig_Agent_Rollback_OnWriteFailure` — mock writeConfig fail → Cfg 回滾到 snapshot
- [ ] `TestArbModeHandler_Get_Snapshot_Shape` — 用 mock Manager return Snapshot{Current:p, Pending:a, EnvLocked:false}；handler 回 JSON `{"current":"passthrough","pending":"authoritative","env_locked":false}`
- [ ] `TestArbModeHandler_Get_Method_Disallowed` — POST/PUT → 405
- [ ] `TestArbModeHandler_Get_Snapshot_UsesSingleCall` — mock Manager 記錄 Snapshot() 被呼叫次數；handler 每次 request 只呼叫一次（不呼叫分欄 getter）
- [ ] `TestAgentModuleWiring_ArbModeManager_ReceivesConfigChange` — 建 module + 觸發 NotifyConfigChange → Manager.Snapshot().Pending 更新（若值不同）

**實作注意**：
- **Config struct 位置**：`internal/config/config.go`（**不是** `internal/core/config.go`）— 現有 `Stream / Detect / Terminal / UploadDir` 等欄位都在這
- **PUT /api/config JSON partial update** 模式在 `internal/core/config_handler.go:27` `configUpdateRequest` struct；參考 `Stream *config.StreamConfig \`json:"stream,omitempty"\`` 既有風格加 `Agent *agentUpdateRequest \`json:"agent,omitempty"\``
- `agentUpdateRequest` struct 用 `ArbMode *string \`json:"arb_mode,omitempty"\`` 指標型態（區分「未提供」vs「零值」）
- 非法值在 `handlePutConfig` validate 段（像 line 52-60 `req.Terminal.SizingMode` 模式）直接回 400
- Snapshot 前的 `snapshot := *c.Cfg` 複製模式要繼續遵守（invariant 寫在 line 80-82）— 新欄位 assignment 必須 wholesale
- Handler 依賴 Manager interface `type Snapshotter interface { Snapshot() Snapshot }`，便於 test mock
- Module wiring：`agent.Module` init 時 `os.Getenv("AGENT_ARB_MODE")` 讀一次 → 建 Manager；註冊 `core.OnConfigChange(func(){ m.OnConfigChange(cfg.Agent.ArbMode) })`
- 路由：`GET /api/agent/arbitrator/mode` — 註冊在 agent module 的 mux（查 `internal/module/agent/handler.go` 現有路由風格對齊）

**Subagent 交付標準**：
- `go test -race ./internal/module/agent/arbmode/... ./internal/config/... ./internal/core/...` 全通過
- 手動驗證：
  - 啟 daemon：`curl -s http://127.0.0.1:7860/api/agent/arbitrator/mode | jq` → `{"current":"passthrough","pending":"passthrough","env_locked":false}`
  - PUT 改值：`curl -X PUT http://127.0.0.1:7860/api/config -H 'Content-Type: application/json' -d '{"agent":{"arb_mode":"authoritative"}}'`
  - 再 GET：`pending="authoritative", current="passthrough"`（未 ApplyAtSessionStart）
  - Env 鎖：`AGENT_ARB_MODE=authoritative ./bin/pdx` 啟 → GET `env_locked=true, current="authoritative"`；PUT 改值 → GET `pending` 保持 authoritative + log 有 `overridden by env`

## 檔案變動總覽

| 檔 | 動作 | Task |
|---|---|---|
| `internal/module/agent/observation/observation.go` | 新增 | 1 |
| `internal/module/agent/observation/observation_test.go` | 新增 | 1 |
| `internal/module/agent/observation/actor_key.go` | 新增 | 1 |
| `internal/module/agent/observation/actor_key_test.go` | 新增 | 1 |
| `internal/module/agent/observation/evidence.go` | 新增 | 1 |
| `internal/module/agent/observation/evidence_test.go` | 新增 | 1 |
| `internal/module/agent/observation/builder.go` | 新增 | 2 |
| `internal/module/agent/observation/builder_test.go` | 新增 | 2 |
| `internal/module/agent/observation/errors.go` | 新增 | 2 |
| `internal/module/agent/observation/trace_id.go` | 新增 | 3 |
| `internal/module/agent/observation/trace_id_test.go` | 新增 | 3 |
| `internal/module/agent/arbmode/mode.go` | 新增 | 4 |
| `internal/module/agent/arbmode/manager.go` | 新增 | 4 |
| `internal/module/agent/arbmode/manager_test.go` | 新增 | 4 |
| `internal/module/agent/arbmode/handler.go` | 新增 | 5 |
| `internal/module/agent/arbmode/handler_test.go` | 新增 | 5 |
| `internal/config/config.go` | 修改 | 5 |
| `internal/config/config_test.go` | 修改 | 5 |
| `internal/core/config_handler.go` | 修改 | 5 |
| `internal/core/config_handler_test.go` | 修改 | 5 |
| `internal/module/agent/module.go`（或對等 wiring） | 修改 | 5 |

**預估**：~650 LOC 含測試（含 EvidenceRef / Prune / Snapshot 追加）

## Verification

1. `cd .claude/worktrees/lights-pr-1b-1a && go test -race ./internal/module/agent/observation/... ./internal/module/agent/arbmode/... ./internal/config/... ./internal/core/...`
2. `go vet ./...`
3. `go build ./...`
4. 手動：起 daemon，`curl -s http://127.0.0.1:7860/api/agent/arbitrator/mode | jq` → `{"current":"passthrough","pending":"passthrough","env_locked":false}`
5. 手動 JSON partial update：
   ```bash
   curl -X PUT http://127.0.0.1:7860/api/config \
     -H 'Content-Type: application/json' \
     -d '{"agent":{"arb_mode":"authoritative"}}'
   ```
   再 GET → `{"current":"passthrough","pending":"authoritative","env_locked":false}`（未 ApplyAtSessionStart）
6. 手動 env 鎖：`AGENT_ARB_MODE=authoritative ./bin/pdx` 起 → GET `{"current":"authoritative","pending":"authoritative","env_locked":true}`；PUT config 改 passthrough → GET 仍 pending=authoritative + daemon log 有 `[arbmode] overridden by env`

## Rollout / Rollback

- **Alpha 階段**：直接 merge 進 main，不加額外 feature gate
- **Rollback**：revert 整個 PR 即可；無 schema 變動、無持久化狀態（TraceIDRegistry in-memory、Manager in-memory）
- **行為影響**：使用者不可見（未接入執行路徑）；daemon 多一個 GET API、多兩個 in-memory struct

## 已知 follow-up（1b-1a 不做，留後續）

| # | 項目 | 歸屬 |
|---|---|---|
| 1 | Arbitrator goroutine 消費 Observation + apply pipeline | PR-1b-1b |
| 2 | `Manager.ApplyAtSessionStart()` 被 Arbitrator 在 SessionStart hook observation 時呼叫 | PR-1b-1b |
| 3 | `TraceIDRegistry.Mint()` 被 Arbitrator 在 SessionStart observation 時呼叫 | PR-1b-1b |
| 4 | `TraceIDRegistry.PruneSessionBefore()` 被 Arbitrator 在 generation 推進後呼叫 | PR-1b-1b |
| 5 | `DecisionPort` 被 truncate 時 metrics counter `lights_decision_ports_truncated` | PR-1b-1b |
| 6 | hook/probe/sweep path 從 direct-write 切到 Observation 路徑 | PR-1b-1c |
| 7 | hook path 的 `trace_id == chain_id` aliasing 汰換（使用 `TraceIDRegistry.Get()`） | PR-1b-1c |
| 8 | `frame_divergences` 實際寫入 | PR-1b-1c |
| 9 | Monitor API envelope 穿透 + SPA `MonitorStep` type 同步（#569）| PR-1b-1c |
| 10 | `TraceIDRegistry.PruneSession()` 被 SessionEnd observation 呼叫 | PR-1b-1c 或更晚 |
| 11 | `POST /api/agent/arbitrator/mode`（runtime 強制切換）| 非 Phase 1 範圍，暫不規劃 |

**#568 何時可關**：PR-1b-1c merge 後（hook path 改用 `TraceIDRegistry.Get()` 取 per-(session, generation) trace_id，完成 end-to-end 交付）。PR-1b-1a/b 只完成 foundation。
