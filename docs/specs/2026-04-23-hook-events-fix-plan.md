# Hook Events PR #616 Fix Plan

- **Date**: 2026-04-23
- **Version**: v4（v1 review 4 + v2 review 3 + v3 review 2 — 共 9 個 findings 全採納；v3 review `review-mobt3cru-xq3lyg` 確認前 7 個已覆蓋，新增 2 個 runtime 安全 finding）
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4（架構護欄）
- **Parent plan**: `docs/specs/2026-04-23-hook-events-declaration-plan.md`（既有，11 commits merged 進本分支）
- **Worktree**: `lights-spec-guardrails`（branch `worktree-lights-spec-guardrails`，PR #616）
- **依賴**: Phase 1 merged at alpha.215（§5 L2 對齊完成）；Events() SSoT 已在本分支建立
- **範圍**: 修四路 codex review 攻擊/防守/體質視角提出的 4 個 findings（2 HIGH + 2 MED），讓 Events() SSoT 主張在 codex 與 opencode 端真正成立，且不因升級誤判既有安裝

## 0. 迭代歷史 — v1 → v2 → v3

### v1 → v2（codex review `review-mobsgj0y-rgwbdv` 4 findings 採納）

| v1 設計 | v2 更正 | 依據 |
|---|---|---|
| `FutureOnly` 過濾 `SupportedStatuses()`（codex 5→2 status） | `SupportedStatuses()` 完全不碰 FutureOnly，保留為 parser capability 宣告（codex 仍 5 status） | `StatusSupporter` 介面註解明定「獨立於 installer 當前覆蓋」；過濾會打破 proxy/未來 CLI 路徑的假陰性 |
| CheckHooks 對 FutureOnly missing 一律靜默 | 區分三態：absent（tolerated）/ present-but-broken（warning + blocks allInstalled）/ valid（pass） | 防止「已配置但損壞」被混成「未安裝但可接受」 |
| drift fixtures 移除 codex 6 個 FutureOnly event 的 9 筆資料 | **保留全部 fixtures**；parser contract 仍持續測；另拆 capability 面向 | DeriveStatus 解析能力不應因 installer 面向而失去回歸保護 |
| Commit 順序: SupportedStatuses 收斂（2）→ drift 升級（4），中間 commit 紅 | drift 測試升級放在 SupportedStatuses 語意切換之前；**每 commit 真正全綠** | 自相矛盾：原 plan 同時要求每 commit 全綠 + 中間依賴後續 commit 補洞 |

核心哲學修正：**`FutureOnly` 只影響 installer/checker 面向，不影響 DeriveStatus 能力宣告面向**。兩個面向獨立。

### v2 → v3（codex review `review-mobsrf0q-47acc5` 3 findings 採納）

| v2 設計 | v3 更正 | 依據 |
|---|---|---|
| State A 定義：「key 不存在 **或 entries 為空**」 | State A 嚴格限為「key 不存在」；entries 空/型別錯/無合法 pdx command → State B (broken) | `mergeCodexHooks` remove 路徑會留下 `hooks[event]=[]`；若歸 absent 則 FutureOnly 會假綠 |
| opencode CheckHooks 用 `extractEmittedEvents` regex + per-event Installed map | **byte-exact template 比對**：從檔案抽 pdxPath，重新 render 後與檔案 byte-equal 比對；不一致 → 整個 unmanaged | regex 會被註解、字串常量、dead code 騙過；plugin 是 pdx 全權受管的原子檔，不應支援 per-event 健康判定 |
| Commit 2 只在 `codexEventSpecs` 標 FutureOnly，宣稱無消費者讀取 → 綠 | Commit 2 同時更新三家 `Events()` defensive copy 帶上 `FutureOnly: spec.FutureOnly`；否則 CE1/CE2 在 commit 2 紅 | 既有 `Events()` 是手寫 field-by-field copy，不會自動帶新欄位 |

核心哲學修正：**opencode plugin 是 atomic managed file，非 per-event 配置**。byte-exact 比對符合 plugin 實際語意，且消除整類 false-green 路徑。

### v3 → v4（codex review `review-mobt3cru-xq3lyg` 2 findings 採納；前 7 findings 已確認覆蓋）

| v3 設計 | v4 更正 | 依據 |
|---|---|---|
| `renderManagedPlugin` 在 template/specs 不一致時 panic | **移除 runtime panic**；template/specs parity 改為 **test-only** 斷言（`TestTemplateSpecsParity`） | `renderManagedPlugin` 被 `writeManagedPlugin`（Install 路徑）與 `CheckHooks`（byte-compare）同時呼叫；runtime panic 會把 build-time 契約錯誤升級成可見程序終止 |
| `extractPdxPath` 直接把 regex 擷取結果重用 | 抓完整 `"..."` quoted literal → `strconv.Unquote(quoted)` 還原後才 pass 給 `renderManagedPlugin` | `renderManagedPlugin` 用 `%q` 寫 pdxPath；regex 抓到的是已 escape 的 JS 源碼；直接 round-trip 含反斜線路徑會二次 escape 假紅 |

核心哲學修正：**契約 drift 是 build-time 問題，test 層面防禦已足；runtime 不做永不恢復的 panic**。

## 1. 契約鎖定

本節鎖定全 Fix 行為，避免實作期再決策。

### 1.1 HookEventSpec 新增 FutureOnly 欄位

`internal/agent/provider.go` `HookEventSpec` 結構加 `FutureOnly bool`：

```
type HookEventSpec struct {
    Name        string
    EmitsStatus []Status
    Description string
    FutureOnly  bool // Installer/checker 面向標記；不影響 DeriveStatus 能力宣告
}
```

**語意（v2 收斂版）**：

- **FutureOnly 只影響 installer / checker 面向**：CheckHooks 對缺省 key 的寬鬆判定、installer 後續可能的「若 CLI 版本不支援則略過」策略（本 PR 不實作動態 gate）
- **不影響 DeriveStatus 能力宣告**：`SupportedStatuses()` / `DeriveSupportedStatuses` 不讀此欄位，codex 仍宣告 5 個 status（Idle/Running/Waiting/Error/Clear）
- **不影響 drift 測試的 parser 契約**：fixture 保留完整，`DeriveStatus` 所有解析路徑持續測
- **預設 false**：`FutureOnly=false` 表示 installer 把此 event 當 required（missing = issue）。在 cc 與 opencode 所有 events 均為 false；僅 codex 6 個 event 顯式設 true

**不把語意等同於「CLI 永遠不會支援」**：只表達「installer 已寫入但 runtime 可能不 emit；checker 對此 missing 不當 bug」。

**Inspector UI 未來如何呈現**：未來若想區分「declared」與「currently reliable」，可以讀 `Events()` 中每個 spec 的 `FutureOnly` bit 自行決定顯示；不耦合進 `SupportedStatuses()`。

### 1.2 DeriveSupportedStatuses 不動

`internal/agent/supported_statuses.go` **不改**。仍為：

```
for _, spec := range specs {
    for _, s := range spec.EmitsStatus { ... }
}
```

不過濾 FutureOnly。codex `SupportedStatuses()` 結果不變。

v1 原本的 S1/S2/S3「skip FutureOnly」測試**移除**。改用 trivial 測試確認「新欄位預設 false + 通過 defensive copy」（§2.1）。

### 1.3 三家 provider FutureOnly 標記矩陣

| Provider | Event | FutureOnly | 理由 |
|---|---|---|---|
| cc | 全部 9 個 | false | cc CLI 實際會 emit 全部 9 個 |
| codex | SessionStart / UserPromptSubmit / Stop | false | 主線 codex CLI hooks 長期支援 |
| codex | SubagentStart / SubagentStop / StopFailure / Notification / PermissionRequest / SessionEnd | **true** | codex CLI 目前不保證 emit；DeriveStatus 已能解析；installer 仍安裝為鋪路 |
| opencode | 全部 8 個 | false | plugin template 實際會 emit 全部 8 個 |

codex `SupportedStatuses()` **維持 5 status**（Idle/Running/Waiting/Error/Clear），因為 §1.2 決定不過濾。

**不做 per-CLI-version gate**：版本探測與動態 FutureOnly 翻轉留給未來 phase。

### 1.4 codex CheckHooks 三態判定

`internal/agent/codex/hooks.go` `CheckHooks` 核心改動：對每個 event 依 FutureOnly 與現況分三態處理。

對每個 event，讀 `hooks.json` 對應 key 得到狀態：
- **State A: absent** — hooks.json **無此 event key**（`hooks` map 中該 key 完全不存在）。**僅此一情形**
- **State B: present-but-broken** — key 存在但任一條件不成立：entries 為空陣列 `[]`、entries 型別非陣列、legacy direct-entry 格式、command 找不到、command 不含合法 pdx path
- **State C: valid** — key 存在、entries 為 matcher-group 陣列、至少一組有合法 pdx command

決策表：

| FutureOnly | State | `events[name].Installed` | `Issues` | 影響 `allInstalled` |
|---|---|---|---|---|
| false | A (absent) | false | append `<name> hook not installed` | **是**（blocks） |
| false | B (broken) | false | append `<name> hook: pdx command not found` | **是**（blocks） |
| false | C (valid) | true | — | 不影響 |
| **true** | A (absent) | false | **不 append**（legacy tolerance） | **否**（不 blocks） |
| **true** | B (broken) | false | **append warning** `<name> hook: pdx command malformed (FutureOnly event has existing hook entry but pdx path incorrect — run install to repair)` | **是**（blocks） |
| **true** | C (valid) | true | — | 不影響 |

核心原則：
- **FutureOnly absent** → 是「legacy 尚未升級」或「runtime 未必需要」的寬鬆態，完全靜默
- **FutureOnly present-but-broken** → 已配置卻壞掉，是確實的 bug，必須顯化並 block（避免「綠燈但實際壞」）
- **FutureOnly valid** → 已升級完成，與 non-FutureOnly 同等認定

實作：遍歷 `p.Events()`（取得含 FutureOnly bit 的 spec slice）。對每個 spec：

1. **Absent check**: `_, keyExists := hooks[spec.Name]` — 若 `keyExists == false` → State A，查表決策
2. Key 存在進一步判斷：
   - `hasLegacyPdxDirectCodexEntry(entries)` → State B (broken)
   - `findPdxCommandInCodex(entries)` → 空字串 → State B (broken)
   - command 非空且為 pdx → State C (valid)
3. 依 spec.FutureOnly 與 state 查表套用決策

**關鍵**：**必須用 `keyExists`（map 的雙回傳值）判斷 absent**，不能用「entries 是否為空」— 空陣列 `[]` 是可落地狀態（`mergeCodexHooks` 移除路徑會留下），歸 broken 方能讓 FutureOnly 顯化。

**3-event legacy 使用者**（升級後）：SessionStart/UserPromptSubmit/Stop = valid（state C），6 個 FutureOnly = absent（state A）→ Issues 空、allInstalled=true。

**主線使用者**（完整 9 event 新安裝）：全部 valid → Installed=true、Issues=[]。

**部分損壞**：若有 FutureOnly key 被手動改壞 → `Issues` 顯化 warning + allInstalled=false（避免假綠燈）。

### 1.5 opencode renderManagedPlugin — 契約即 SSoT（test-only parity check）

Finding #2（PR #616 第 2 輪防守 HIGH + 體質 MED）核心：不讓 template 與 `opencodeEventSpecs` 成為兩份平行 SSoT。

策略（v4 收斂）：template 保留固定 switch/case 結構；**契約檢查只在 test 層面執行**；`renderManagedPlugin` **不做任何 runtime validation、不 panic**，純粹 render 字串後返回。

具體：

- `renderManagedPlugin(pdxPath string) string` — 維持原簽名、原行為；**不呼叫 validate、不 panic**
- `extractEmittedEvents(body string) []string` — regex `emit\(['"](\w+)['"]` 從 body 抽出 emit 的 event names。**只由測試使用**
- `validateSpecsCoverEmitted(body string, specs []HookEventSpec) error` — 強等：`set(extractEmittedEvents(body)) == set(specs.Name)`；任何一側 superset 都回 error。**只由測試使用**
- **測試層強制 parity**：在 `plugin_template_test.go` 增加 `TestTemplateSpecsParity`，對 `renderManagedPlugin("/fake")` 的結果跑 `validateSpecsCoverEmitted`，不等則 test fail

**為什麼移除 runtime panic**（v4 決定）：`renderManagedPlugin` 被 `writeManagedPlugin`（Install 路徑）和 `CheckHooks`（byte-compare 重新 render）呼叫；runtime panic 會把 build-time 契約錯誤升級成可見程序終止。test-only 防線已足以擋下 drift — 測試通過表示 specs 與 template 對齊，合併後不可能出現 mismatched 版本。

**`extractEmittedEvents` 角色**：**僅供測試使用**。runtime 健康檢查（CheckHooks）走 §1.6 的 byte-exact 比對。regex 對 JS 靜態分析不可靠，本就不適合 runtime。

**配合測試**（§2.3 PT1-PT5）驗證。

### 1.6 opencode CheckHooks — byte-exact template 比對

`internal/agent/opencode/hooks.go` `CheckHooks` 策略（v3 重新設計）：

**Plugin 是 pdx 全權受管的原子 artifact，非 per-event 配置檔**。CheckHooks 採「template body byte-exact」語意：

1. 讀檔（不變）
2. 驗 marker（不變）
3. 從檔案抽 `pdxPath`（template 內有 `const pdxPath = "..."` 可用 regex `const pdxPath = "([^"]+)"` 抽出）
4. 以抽出的 pdxPath 重新 render template → `expected`
5. **byte-exact 比較** `data == expected`：
   - 相等 → `Installed=true`；每個 `opencodeEventSpecs` event 的 `events[name] = {Installed: true, Command: pluginPath}`；`Issues=[]`
   - 不等 → `Installed=false`；issue 含 `plugin body differs from managed template (run reinstall)`；**所有 event `Installed=false`**（因為整份 plugin 視為不可信）
6. 若 marker 不存在 → 既有 unmanaged 行為保留

**新 helper**：`extractPdxPath(body string) (string, bool)` — 從受管 body 抽 pdxPath。

**關鍵**：template 以 `%q` 寫 pdxPath，反斜線、引號、非 ASCII 都已被 Go 字串 escape。regex 必須抓**完整 quoted literal**（含前後雙引號），再用 `strconv.Unquote` 還原成原始路徑：

1. regex `const pdxPath = (\"(?:[^\"\\\\]|\\\\.)*\")` — 捕獲含跳脫處理的完整 `"..."` 字串（包含外層雙引號），支援 `\\` `\"` `\n` 等 escape sequence
2. 對捕獲結果呼叫 `strconv.Unquote(quoted)` — Go string quoting 規則與 `%q` 對稱，含 `\`、`\"`、Unicode escape 都能還原
3. `Unquote` 失敗（格式錯誤或找不到匹配）→ 回 `("", false)` → CheckHooks 視為 byte-mismatch

**為什麼不能直接用 regex `"([^"]+)"` 抓內容**：該 regex 會在遇到 `\"` 時提早結束，抽到殘缺字串；再 render 會二次 escape 假紅。

**測試覆蓋**（PT6 擴充）：round-trip 必須對以下路徑全綠：
- 空白：`/path with spaces/pdx`
- 反斜線（Windows-style）：`C:\Users\foo\pdx.exe`
- 雙引號（罕見但合法）：`/weird"path/pdx`
- 非 ASCII：`/使用者/pdx`
- 控制字元不測試（install 路徑不會產生）

**為什麼不 per-event**：opencode plugin 是單檔 JavaScript，switch/case 結構內任何改動都可能影響其他 event（例 shared state `activeSubagents`、`suppressIdleForSession`）。將 plugin 視為「有 marker + byte-equal」二元狀態符合其語意：要嘛 pdx 受管、要嘛使用者自行負責。

**效果**：完全消除 regex-based 假綠燈：
- 註解內 `emit('Fake')` → 改動 → byte-mismatch → unmanaged
- dead code 分支 → 改動 → byte-mismatch
- 人為改 `switch` 邏輯但保留 emit string → byte-mismatch
- 完全合法 pdx 受管 body → byte-equal → all installed

**測試簡化**（§2.4 OH1-OH3）— v2 的 OH4 (unexpected emit) 不再需要（byte-exact 覆蓋全部異動 case）。

### 1.7 drift test per-event 強相等 + parser contract 保留

`internal/agent/drift_test.go` 改動原則（v2 收斂）：

- **保留所有既有 fixture**（含 codex 6 個 FutureOnly event 的 9 筆）— parser contract 不退化
- **新增 per-event 強相等斷言**：抓單一 event 過度宣告（Finding #4）
- **所有斷言涵蓋 FutureOnly event**：SupportedStatuses 未過濾，declaredSet 也不過濾，所以三向相等天然包含 FutureOnly
- **不新增 D6 防呆**（v1 曾提議「fixture 不得含 FutureOnly」）— v2 移除該設計；FutureOnly fixture 是刻意的 parser contract 保護

具體改動：

- `TestDriftThreeWayEquality` — **不動**。仍驗三向相等；codex declared/emitted/supported 皆為 5 status set。
- `TestDriftFixtureCoversAllEvents` — **不動**。要求所有 `Events()` event 都有 fixture（含 FutureOnly）。
- `TestDriftFixtureCoverageNonEmpty` — **不動**。
- **新增 `TestDriftPerEventEmitsStatusMatch`**：對每個 provider 的每個 spec（含 FutureOnly），取該 event name 的所有 fixtures，跑 DeriveStatus 收集 `emitSet`（Valid=true && Status != ""），斷言 `emitSet == set(spec.EmitsStatus)`（雙向強相等）。抓單一 event 的 EmitsStatus 過度或不足宣告。

`providerFixtures` **不變**（v1 曾計畫移除 codex 9 筆 FutureOnly fixture；v2 撤回）。

**驗收 D5 有效性**：人為測試把 codex `Stop` spec 的 `EmitsStatus` 從 `[Idle]` 改為 `[Idle, Clear]` → `TestDriftPerEventEmitsStatusMatch` 應紅（Stop fixture 只 emit Idle，spec 多宣告 Clear）。

### 1.8 opencode events.go 對齊 sanity check

從 `plugin_template.go` renderManagedPlugin 靜態分析：
- template emit: `SessionStart`, `PermissionRequest`, `StopFailure`, `Stop`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`（8 個）
- `opencodeEventSpecs.Name`：同上 8 個

**相等**。§1.5 validate 不會 panic。

### 1.9 零改動邊界

以下檔案 Fix **不得觸碰**：

- `internal/agent/registry.go`, `status.go`, `coverage.go`, `hook_version.go`, `process_info*.go`
- `internal/agent/supported_statuses.go`（v2 移除修改此檔的計畫）
- `internal/agent/cc/**` 全部（cc 所有 event FutureOnly=false 由預設值保證 — 可不顯式標）
- `internal/agent/opencode/status.go`, `provider.go`, `readiness.go`, `events.go`（opencode events 無 FutureOnly 標記 — 預設 false）
- `internal/agent/codex/status.go`, `provider.go`, `readiness.go`, `provider_test.go`（v1 要求 codex supported 測試 5→2 — v2 撤回，provider_test 不動）
- `internal/agent/probe/**`
- `internal/store/**`, `internal/module/**`, `spa/**`

## 2. 測試案例清單

### 2.1 FutureOnly 欄位單元測試

`internal/agent/supported_statuses_test.go` 既有 case 不變。**不新增**「skip FutureOnly」測試（v2 移除）。

`internal/agent/provider_test.go`（若不存在則在 `supported_statuses_test.go` 加）新增：

| # | 名稱 | 斷言 |
|---|---|---|
| F1 | `TestHookEventSpecFutureOnlyDefaultsToFalse` | 宣告一個不指定 FutureOnly 的 HookEventSpec → `FutureOnly=false` |
| F2 | `TestDeriveSupportedStatusesIgnoresFutureOnlyBit` | specs 含 FutureOnly=true + EmitsStatus=[Waiting] → Waiting 仍出現在結果（防止未來被誤加過濾 logic） |

F2 是防呆測試，鎖定「DeriveSupportedStatuses 不因 FutureOnly 過濾」的契約。

### 2.2 codex Events 欄位與 CheckHooks FutureOnly 行為

`internal/agent/codex/events_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| CE1 | `TestCodexEventsFutureOnlyFlags` | 3 個非 FutureOnly（SessionStart/UserPromptSubmit/Stop）+ 6 個 FutureOnly，一一 assert `spec.FutureOnly` bit |
| CE2 | `TestCodexEventsDefensiveCopyPreservesFutureOnly` | 取兩次 `Events()`、mutate 第一次的 FutureOnly → 第二次不受影響 |

`internal/agent/codex/hooks_test.go` 新增（用既有 writing temp hooks.json + pdx path 的 helper 模式；若 helper 不存在則仿 cc 的 pattern）：

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| CH1 | `TestCheckHooks_LegacyThreeEvent_ReportsInstalled` | hooks.json 僅 SessionStart/UserPromptSubmit/Stop valid（FutureOnly 6 absent） | `allInstalled=true`、`Issues=[]`、6 個 FutureOnly `Events` map entry `Installed=false` |
| CH2 | `TestCheckHooks_NineEvent_FullyInstalled_NoIssues` | hooks.json 9 events 全 valid | `allInstalled=true`、`Issues=[]`、9 個 entry `Installed=true` |
| CH3 | `TestCheckHooks_RequiredEventMissing_Blocks` | 只有 2 個 required event valid（缺 SessionStart） | `allInstalled=false`、Issues 含 `SessionStart hook not installed` |
| CH4 | `TestCheckHooks_FutureOnlyBroken_WarnsAndBlocks` | hooks.json 3 required valid + 1 FutureOnly（Notification）key 存在但 command 錯誤 | `allInstalled=false`、Issues 含 `Notification hook: pdx command malformed...`、Notification Installed=false |
| CH5 | `TestCheckHooks_FutureOnlyAbsent_DoesNotBlock` | hooks.json 3 required valid + 3 FutureOnly absent + 3 FutureOnly valid | `allInstalled=true`、Issues=[]、3 absent entries Installed=false、3 valid entries Installed=true |
| CH6 | `TestCheckHooks_FutureOnlyEmptyArray_ClassifiesAsBroken` | hooks.json 3 required valid + 1 FutureOnly key 存在但 entries `[]`（空陣列） | `allInstalled=false`、Issues 含 `<name> hook: pdx command malformed...`、該 event `Installed=false`（不能因空陣列歸 absent 假綠） |

### 2.3 opencode template render-time contract validation

`internal/agent/opencode/plugin_template_test.go` 新增（只測 build-time 契約，不測 runtime 健康）：

| # | 名稱 | 斷言 |
|---|---|---|
| PT1 | `TestExtractEmittedEvents` | helper 從 sample body 正確抽出 event names（含 regex corner：單引號、雙引號、多行、插值） |
| PT2 | `TestValidateSpecsCoverEmitted_Equal` | `validateSpecsCoverEmitted(body, opencodeEventSpecs)` 對真 template body 回 nil |
| PT3 | `TestValidateSpecsCoverEmitted_EmitNotInSpec` | synthetic body 含 `emit('Fake')` + specs 無 Fake → error |
| PT4 | `TestValidateSpecsCoverEmitted_SpecNotInEmit` | synthetic body 缺某 event + specs 含它 → error |
| PT5 | `TestRenderManagedPlugin_ProducesValidBody` | `renderManagedPlugin("/p")` 不 panic；結果 body 含 marker |
| PT6 | `TestExtractPdxPath_RoundtripEscapedLiterals` | 對下列 pdxPath 驗證 `render → extract → equals input`：`/path with spaces/pdx`、`C:\Users\foo\pdx.exe`、`/weird"path/pdx`、`/使用者/pdx` |
| PT7 | `TestTemplateSpecsParity` | 對 `renderManagedPlugin("/fake")` 呼叫 `validateSpecsCoverEmitted(body, opencodeEventSpecs)` → nil（build-time contract guard）|

### 2.4 opencode hooks CheckHooks — byte-exact template 比對

`internal/agent/opencode/hooks_test.go` 新增（v3 簡化：plugin 是 atomic managed file）：

| # | 名稱 | 斷言 |
|---|---|---|
| OH1 | `TestCheckHooks_ValidPlugin_AllInstalled` | 用 `writeManagedPlugin` 寫合法 template → CheckHooks 所有 event `Installed=true`、Issues=[] |
| OH2 | `TestCheckHooks_HandEditedPlugin_Unmanaged` | 寫一份含 marker 但改動一個字（例移除某 `emit('Stop',`） → CheckHooks **整個 plugin** `Installed=false`、Issues 含 `plugin body differs from managed template`、**所有 event `Installed=false`** |
| OH3 | `TestCheckHooks_UnmanagedPlugin_NoMarker` | 既有行為保留：不帶 marker → Installed=false + Issues `plugin file exists but is unmanaged` |
| OH4 | `TestCheckHooks_CommentEditInPlugin_DetectedViaByteCompare` | 寫合法 template 後**只加一行註解** → byte-mismatch → Installed=false（驗證 regex 假綠盲點已由 byte-exact 覆蓋） |
| OH5 | `TestCheckHooks_ExtractPdxPathFails_FallsBackToUnmanaged` | 寫檔案含 marker 但 `const pdxPath` 被改壞（regex 抽不到）→ CheckHooks 不 panic、回 Installed=false + issue |

**刪除**：v2 OH4 (per-event unexpected emit detection) — byte-exact 已天然覆蓋。

### 2.5 drift test per-event 強相等

`internal/agent/drift_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| D5 | `TestDriftPerEventEmitsStatusMatch` | 對每個 provider 的每個 spec：取該 event 的 fixtures → emitSet == set(spec.EmitsStatus) |

D5 天然覆蓋 FutureOnly event（因為 fixtures 與 declaredSet 都包含）。**無 D6**（v1 的 FutureOnly fixture 禁令移除）。

既有 D1/D2/D3/D4 不動。`providerFixtures` 不變。

## 3. TDD 執行順序

嚴格紅綠，每個 step 一個 commit；commit message 用 Conventional Commits + `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`。

**關鍵**：每 commit 完跑 `go build ./... && go test ./... && go vet ./...` **必須全綠**。v2 下這可滿足，因為：
- DeriveSupportedStatuses 不變 → codex SupportedStatuses 仍 5 status → drift ThreeWayEquality 不會因 FutureOnly 標記而破
- fixtures 不移除 → parser drift 不退化
- CheckHooks 內部行為變化只影響 codex/hooks_test.go 本身

### Commit 1 — `feat(agent): add FutureOnly bit to HookEventSpec and defensive copies`
- 紅：寫 F1 + F2 + CE2 的 defensive copy 預斷言 → 失敗
- 綠：
  1. `provider.go` 加 `FutureOnly bool` 欄位 + 註解（§1.1）
  2. `DeriveSupportedStatuses` **不動**
  3. **三家 `Events()` defensive copy 同步加 `FutureOnly: spec.FutureOnly`**：
     - `cc/events.go` Events()
     - `codex/events.go` Events()
     - `opencode/events.go` Events()
  4. F1/F2 綠；defensive copy 預檢查：既有測試（若有直接讀 `Events()` 的）不受影響（因為此 commit 尚未標任何 FutureOnly=true，所有 bit 仍預設 false）
- 跑 `go test ./internal/agent/...` 全綠

**為什麼 Commit 1 就改三家 `Events()`**：v2 Commit 2 宣稱「只標 `codexEventSpecs` 就綠」是錯的 — `Events()` 是手寫 field copy，若不先補 FutureOnly 傳遞，Commit 2 的 CE1/CE2 會直接紅（spec 欄位有但 copy 沒帶 → 測試測到的是 copy 後的 struct，會看到 false）。Commit 1 一併處理。

### Commit 2 — `feat(agent/codex): mark 6 future-only events`
- 紅：寫 CE1 → 失敗（6 個 event 尚未設 `FutureOnly: true`）
- 綠：`codex/events.go` 把 SubagentStart / SubagentStop / StopFailure / Notification / PermissionRequest / SessionEnd 六個加 `FutureOnly: true`
- 補：CE2 完整實作（defensive copy mutate 後獨立性；依賴 Commit 1 已修好的 copy）
- 跑 `go test ./internal/agent/codex/...` 綠
- 跑 `go test ./internal/agent/...` 全綠（drift 三向相等仍成立：SupportedStatuses 不讀 FutureOnly）

### Commit 3 — `feat(agent/codex): three-state CheckHooks with FutureOnly awareness`
- 紅：寫 CH1-CH6 → 失敗（CheckHooks 尚未 aware）
- 綠：`codex/hooks.go` 改 CheckHooks 遍歷用 `p.Events()`（取得 FutureOnly bit），決策表依 §1.4 三態（**嚴格以 `_, keyExists := hooks[spec.Name]` 判 absent；entries 空陣列歸 broken**）
- 建議抽 helper `checkCodexEvent(spec HookEventSpec, rawEntries any, keyExists bool) (HookEventInfo, []string, bool /*blocks*/)` 便於單點測試（CH1-CH6 可呼叫或仍走 CheckHooks 整體 — subagent 依測試可讀性擇一）
- 跑 `go test ./internal/agent/codex/...` 綠

### Commit 4 — `test(agent): per-event drift assertion`
- 紅：寫 D5 → 對既有 spec/fixture 跑起來可能紅（若有 over-declaration）或直接綠
- 綠：若紅，修 spec 或 fixture 對齊（最可能 case：cc 的 `Notification` EmitsStatus `[Waiting, Idle]` 對 fixtures 的 4 筆全綠 — 應直接綠）
- 跑 `go test ./internal/agent/...` 綠

### Commit 5 — `refactor(agent/opencode): extract emitted events + pdx path helpers`
- 紅：寫 PT1 + PT6 → 失敗（helpers 不存在）
- 綠：`plugin_template.go` 加
  - `extractEmittedEvents(body string) []string`（regex `emit\(['"](\w+)['"]`）— **僅供測試**
  - `extractPdxPath(body string) (string, bool)` — regex 抓完整 quoted literal `const pdxPath = (\"(?:[^\"\\\\]|\\\\.)*\")`，用 `strconv.Unquote` 還原（§1.6 細節）
- PT6 必須覆蓋 space / backslash / quote / 非 ASCII 四種 escape round-trip
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 6 — `test(agent/opencode): template ↔ specs parity (test-only)`
- 紅：寫 PT2 + PT3 + PT4 + PT5 + PT7 → 失敗
- 綠：
  1. 加 `validateSpecsCoverEmitted(body string, specs []HookEventSpec) error`（package-internal，**僅供測試使用**）
  2. **`renderManagedPlugin` 不動**（不呼叫 validate、不 panic）
  3. PT7 在 test 層跑 parity check — 即 build-time guard（若 template/specs 漂移 → 測試紅，阻止 merge）
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 7 — `feat(agent/opencode): byte-exact CheckHooks against managed template`
- 紅：寫 OH1 + OH2 + OH3 + OH4 + OH5 → 失敗（CheckHooks 仍用舊邏輯）
- 綠：`opencode/hooks.go` CheckHooks 依 §1.6 改寫：
  1. 讀檔 + 驗 marker（既有）
  2. `extractPdxPath(body)` — 若抽不到 → unmanaged 風格 issue
  3. `expected := renderManagedPlugin(pdxPath)`
  4. `bytes.Equal(data, []byte(expected))` → 所有 event Installed=true 或全部 Installed=false + `plugin body differs from managed template` issue
- 刪除舊 per-event extraction 用於 CheckHooks 的程式路徑（若 Commit 5 有順手掛到 CheckHooks，在此 commit 明確清除）
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 8 — `docs: hook events fix plan retrospective（可選）`
若 §3 順序與實際有偏差，補歷史註記。

**Commit 綠燈分析**（v3 收斂）：
- Commit 1 → 加 FutureOnly 欄位 + 三家 `Events()` defensive copy 同步；DeriveSupportedStatuses 不變；codex SupportedStatuses 5 status 不變；drift 測試仍綠。F1/F2 綠
- Commit 2 → 6 個 codex event 標 FutureOnly=true；無消費者讀取（CheckHooks 要等 Commit 3、drift 不過濾、SupportedStatuses 不讀）→ 所有既有測試行為不變；CE1/CE2 綠（Commit 1 已補好 defensive copy 傳遞）
- Commit 3 → CheckHooks 三態行為變，但只影響 `codex/hooks_test.go`；新增 CH1-CH6 綠
- Commit 4 → D5 新增；既有 D1/D2/D3/D4 + fixtures 不變
- Commit 5/6 → opencode helpers + render-time validation；runtime 路徑不變
- Commit 7 → opencode CheckHooks 替換；OH1-OH5 綠
- **每一 commit 後全套 go test 綠**（v3 已驗證依賴鏈封閉）

## 4. 實作檔案預估

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/provider.go` | +`FutureOnly bool` 欄位 + 註解 | +8 |
| `internal/agent/supported_statuses_test.go` 或 `provider_test.go` | +F1/F2 | +25 |
| `internal/agent/cc/events.go` | Events() defensive copy 加 `FutureOnly: spec.FutureOnly` | +1 |
| `internal/agent/codex/events.go` | 6 event 加 `FutureOnly: true` + Events() defensive copy 同步 | +7 |
| `internal/agent/codex/events_test.go` | +CE1/CE2 | +40 |
| `internal/agent/codex/hooks.go` | CheckHooks 三態決策 + `checkCodexEvent` helper | +40 |
| `internal/agent/codex/hooks_test.go` | +CH1-CH6 | +140 |
| `internal/agent/opencode/events.go` | Events() defensive copy 加 `FutureOnly: spec.FutureOnly` | +1 |
| `internal/agent/opencode/plugin_template.go` | +`extractEmittedEvents` + `extractPdxPath` (含 Unquote) + `validateSpecsCoverEmitted`（**無 runtime panic**） | +50 |
| `internal/agent/opencode/plugin_template_test.go` | +PT1-PT7（PT6 escape round-trip / PT7 parity） | +120 |
| `internal/agent/opencode/hooks.go` | CheckHooks byte-exact template 比對 | +30 |
| `internal/agent/opencode/hooks_test.go` | +OH1-OH5 | +100 |
| `internal/agent/drift_test.go` | +D5（無 fixture 刪除、無 FutureOnly skip） | +45 |
| `docs/specs/2026-04-23-hook-events-fix-plan.md` | 本檔 v3 | +480 |
| **合計** | | **~1050 行**（含 plan 文件） |

## 5. 不做項目清單（防自動擴展）

以下衝動一律拒絕：

- **不過濾 FutureOnly 於 `SupportedStatuses()` 或 `DeriveSupportedStatuses`** — v2 核心決定；F2 測試鎖定此契約
- **不移除任何既有 fixture** — parser contract 必須持續測
- **不新增「fixture 禁含 FutureOnly」測試** — v1 D6 移除
- **不改 codex `provider_test.go` SupportedStatuses 斷言**（仍 5 status）
- **不探測 codex CLI 版本** — FutureOnly 是 build-time 靜態標記
- **不引入 `/api/hooks/codex/capability` endpoint**
- **不重寫 opencode template 為完全 codegen**（v3 確認：template 保固定結構 + **test-only** parity validate，v4 移除 runtime panic）
- **renderManagedPlugin 不做 runtime validation / panic**（v4：contract drift 由測試層防禦，runtime 純 render）
- **不用 naive regex `"([^"]+)"` 抓 pdxPath**（v4：必抓完整 quoted literal + strconv.Unquote）
- **不對 opencode CheckHooks 做 per-event extraction**（v3 決定：byte-exact template 比對）
- **不接受 codex absent 定義包含空陣列**（v3 嚴格限：`_, ok := hooks[key]; ok == false` 才是 absent）
- **不把 cc 任何 event 標為 FutureOnly**
- **不調整 `Coverage()` 簽名或回傳欄位**
- **不改 SPA 任何檔案**
- **不 force push / rebase** — 保留全歷史
- **不合併 fix commits** — 分 7 commits 便於 reviewer 逐步追蹤
- **不順手修其他 finding** — 只修原 4 findings + v2/v3 延伸的 7 findings 全部列出；其餘盲點開 gh issue 延後

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-spec-guardrails && ` 開頭
- **分支**：已在 `worktree-lights-spec-guardrails`，不另切；**不 push**（主 session 負責）
- **Commit message 格式**：Conventional Commits + 結尾 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **TDD 嚴格紅綠**：每 commit 內先寫測試跑紅再實作跑綠；不可批次寫完所有 test + 一次實作
- **每 commit 完成後**：跑 `go build ./... && go test ./... && go vet ./...` 全綠才進下一個；**不可中間紅燈**
- **關鍵抽 helper 點**：
  - codex `CheckHooks`：建議新增私有 `checkCodexEvent(spec HookEventSpec, rawEntries any) (HookEventInfo, issues []string, block bool)` helper 把三態判定抽出，避免 CheckHooks 變 60+ 行巨型函式
  - opencode `CheckHooks`：`extractEmittedEvents` + `validateSpecsCoverEmitted` 都已是 helper
- **回報**：完成時給 commit hash 列表 + `git log --oneline -15` + 最終 `go test ./...` 完整輸出 + `go vet ./...` 無 warning 確認

## 7. 驗收清單

- [ ] 7 個 commits 符合上述 message 規範
- [ ] `go build ./...` 綠
- [ ] `go test ./...` 全綠（含 F1-F2 / CE1-CE2 / CH1-CH6 / D5 / PT1-PT7 / OH1-OH5 共 23 個新測試）
- [ ] `go vet ./...` 無 warning
- [ ] PR diff 僅涉及 §4 表格列出的 14 個檔 + plan 文件，**其餘不得出現**
- [ ] codex `SupportedStatuses()` 仍回傳 5 個 Status（不動）
- [ ] codex `Events()` 中 6 個 FutureOnly event 的 bit 為 true，其餘 false；defensive copy 保留 bit（mutate 第一次不影響第二次）
- [ ] codex `CheckHooks()` 對 legacy 3-event hooks.json 回 `allInstalled=true` + Issues=[]
- [ ] codex `CheckHooks()` 對「FutureOnly key 存在但 command 錯誤」回 `allInstalled=false` + warning issue
- [ ] codex `CheckHooks()` 對「FutureOnly key 存在 entries 為 []」歸 broken 而非 absent（**v3 新驗收點**）
- [ ] opencode `CheckHooks()` 對**任何**手改過的 plugin（包含註解變動）回 `Installed=false` + `plugin body differs` issue（byte-exact）
- [ ] opencode `renderManagedPlugin` **不 panic**（任何路徑）；template/specs parity 由 `TestTemplateSpecsParity` test-only guard
- [ ] opencode `extractPdxPath` 對含 space/backslash/quote/非 ASCII 的路徑能 round-trip（PT6）
- [ ] drift per-event 斷言（D5）對每個 provider/spec 覆蓋正確

## 8. 風險與緩解

| 風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| FutureOnly 語意仍可能被後續開發者誤用為 SupportedStatuses 過濾 | 中 | 未來 regression | F2 測試鎖定契約；`provider.go` FutureOnly 欄位註解明寫「不影響 DeriveStatus 能力宣告」 |
| CheckHooks 三態決策複雜度累積 | 低 | 新增 FutureOnly 時 regression | 抽 `checkCodexEvent` helper；CH1-CH6 覆蓋所有交叉組合 |
| `extractEmittedEvents` regex 對 CheckHooks 的假綠風險 | **已消除** | n/a | v3 決定：`extractEmittedEvents` 只用於 render-time contract check；CheckHooks 走 byte-exact |
| opencode byte-exact 太嚴格：使用者若自行 patch plugin 會立刻回 unmanaged | 中 | UX | 這是預期行為 — plugin 是 pdx 受管原子檔。若有合法 customization 需求，未來另開 `unmanaged` 模式 |
| `extractPdxPath` regex 遇到 odd-formed pdxPath 抽不到 | 低 | CheckHooks fallback unmanaged | OH5 覆蓋此 case；fallback 回 unmanaged 而非 panic |
| opencode render panic 在 production 路徑誤觸 | **已消除** | n/a | v4：renderManagedPlugin 不 panic；contract drift 由 TestTemplateSpecsParity test-only guard |
| `extractPdxPath` 對特殊字元路徑 round-trip 失敗 | **已消除** | n/a | v4：strconv.Unquote 處理完整 quoted literal；PT6 覆蓋 4 種 escape case |
| drift fixtures 保留後，若 FutureOnly event 在 DeriveStatus 邏輯日後被刪除但 fixture 沒刪 | 低 | 測試失敗警告到位 | D5 per-event 強相等會紅，指向確切 spec/event；是有益的警告 |
| commit 4 D5 對既有 spec/fixture 跑起來紅（有過度宣告） | 中 | 需額外修 spec 或 fixture | 先跑一次 D5 preview；若紅先修 spec 再加 D5 斷言 |
| 三家 `Events()` defensive copy 改動 commit 1 執行時漏改某家 | 中 | commit 2 紅（CE1/CE2） | Commit 1 明列 3 個檔案 + 紅綠 TDD 會立即暴露 |

## 9. 第 3 輪 Codex Review 預期 focus

**Focus text 骨架**（派發時套 4 findings + v2 設計決策）：

- 本輪修復 PR #616 第 2 輪 3 路 review 的 4 findings（+ plan review 4 points）：
  1. codex CheckHooks 對 3-event legacy 使用者誤判（透過 FutureOnly + 三態判定）
  2. opencode renderManagedPlugin 與 CheckHooks 的假綠燈（透過 extractEmittedEvents + validate + CheckHooks per-event）
  3. codex future-only events 混入 current capability（透過 FutureOnly bit，**但不影響 SupportedStatuses**；留給 Inspector UI 自行判讀）
  4. drift test per-event 盲點（透過 D5 per-event 強相等）
- 請驗收：
  - 攻擊：FutureOnly 三態決策的邊界（absent/broken/valid × required/optional）；extractEmittedEvents regex 假陽性；opencode render panic 是否能在 release path 誤觸
  - 防守：SSoT 主張是否仍成立（FutureOnly 不削減 SupportedStatuses，那 Inspector 要怎麼知道 codex 目前只能 emit 哪些？— plan 有無明確交代 future 路徑）
  - 體質：fix plan 拆的 7 commits 是否職責清晰；drift_test 新 D5 是否過大；hooks.go `checkCodexEvent` helper 是否正確抽出三態判定

第 3 輪仍需 4 路（標準 + 攻擊 + 防守 + 體質）。
