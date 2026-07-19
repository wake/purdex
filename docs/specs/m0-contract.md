# M0 Wire Contract — Ploom ↔ Purdex 派工（SOT）

> **這是兩 repo 的唯一真相**。任一側改契約 = 改本檔 = 改 `docs/fixtures/m0/*.json` = 雙側重跑測試。
> **依據 spec**：`docs/specs/2026-07-19-m0-dispatch-integration.md`（commit `19e622b` 版）§3/§4/§8.1。
> **傳輸**：pull，全 daemon 發起（Ploom 純 server，無 Ploom→Purdex callback）。
> **Auth**：daemon 綁 is_agent 帳號的 S4 Bearer token。
> **`schema_version`**：M0 = `1`；所有 payload 必帶；不相容 → `schema_incompatible` 明確拒（非 silent）。

---

## 1. Endpoints 總表（pull）

| # | Method + Path | 作用 | 兩段式 |
|---|---------------|------|--------|
| E1 | `GET /daemon/dispatches?status=pending` | 輪詢領工最小清單 | ① |
| E2 | `POST /daemon/dispatches/{id}/claim` | 原子 pending→claimed | — |
| E3 | `GET /daemon/dispatches/{id}` | 領工後抓完整 issue + repo_location + sandbox_profile | ② |
| E4 | `POST /daemon/dispatches/{id}/report` | 回報 execution 狀態/artifact，回 `ack_seq` | — |

隔離：E1/E3 **只回屬於 caller daemon 的**（`daemon_id` + 帳號 membership 雙層）；dispatch 錨點 authz = issue project **editor+**。

---

## 2. 欄位字典（防漏 — 每欄位明列）

| 欄位 | 型別 | 出現於 | 說明 |
|------|------|--------|------|
| `schema_version` | int | 全 payload | M0 = 1 |
| `dispatch_id` | string(`dsp_…`) | E1/E2/E3/E4(echo) | Ploom 派工單位 |
| `issue_id` | string | E1/E3 | dispatch 掛的 issue |
| `execution_id` | string(`exc_…`) | E4 | **唯一對外穩定 handle**（Purdex 生） |
| `attempt_no` | int | E4 | M0 恆 `1`（欄位保留，attempt 資料層 M3） |
| `provider` | enum | E4 | M0 恆 `"claude"`（中立接縫，codex → M1） |
| `status` | enum | E4 | `accepted`/`running`/`completed`/`failed`（liveness = accepted\|running） |
| `seq` | int | E4(request) | per-execution **單調遞增**，`accepted`=1 |
| `ack_seq` | int | E4(response) | Ploom 已投影到的最大 seq |
| `repo_location` | object | E3 / E4(echo) | S5 既有：`{project_id, remote_url?, owner?, repo?, local_dir, is_origin}` |
| `effective_sandbox_profile` | enum | E4(accepted) | clamp 後實際 profile（見 §6） |
| `sandbox_profile` | enum | E3(request 值) | dispatch **request** 的 profile（只能被 clamp 降） |
| `head_at_start` | string(sha) | E4(accepted) | 受理時 repo HEAD commit（diff base） |
| `dirty_at_start` | bool | E4(accepted) | 受理時 working tree 是否 dirty |
| `session_code` | string(6-base36) | E4(nullable) | 衍生 deeplink handle（tmux 建立後 EncodeSessionID；**非** crash-recovery handle） |
| `deeplink` | string(`purdex://…`) | Ploom 投影（E4 後組） | `purdex://execution/<id>`（`?host=` optional hint） |
| `artifacts[]` | array | E4(completed/failed) | pointer-first，見 §5 |
| `error` | object | E4(failed) / 各 4xx | `{code, message}` |

---

## 3. 各 Endpoint 形狀

### E1 `GET /daemon/dispatches?status=pending`
**Response 200**（最小清單，只 caller daemon）：
```json
{ "schema_version": 1,
  "dispatches": [ { "dispatch_id": "dsp_a1", "issue_id": "iss_42" } ] }
```

### E2 `POST /daemon/dispatches/{id}/claim`
- **成功**：`200 {schema_version, dispatch_id, status:"claimed"}`
- **同 daemon 重複 claim（冪等）**：`200 {…, status:"claimed", execution_id?}`（若已建 execution 則帶回）
- **他 daemon 已 claim**：`409 {error:{code:"already_claimed"}}`
- **非本 daemon 的 dispatch**：`404 {error:{code:"not_owner"}}`（reach 不到即 404，fail-closed）

### E3 `GET /daemon/dispatches/{id}`（兩段式②）
**Response 200**：
```json
{ "schema_version": 1, "dispatch_id": "dsp_a1",
  "issue": { "issue_id": "iss_42", "title": "...", "body": "..." },
  "repo_location": { "project_id":"prj_1","local_dir":"/abs/repo","is_origin":true },
  "sandbox_profile": "workspace-write" }
```

### E4 `POST /daemon/dispatches/{id}/report`
Request 依 `status` 攜帶不同欄位（見 §4 ordering）：
```json
// accepted (seq=1, 攜 immutable metadata)
{ "schema_version":1, "dispatch_id":"dsp_a1", "execution_id":"exc_9",
  "attempt_no":1, "provider":"claude", "status":"accepted", "seq":1,
  "effective_sandbox_profile":"ask", "head_at_start":"abc123",
  "dirty_at_start":false, "session_code":null }
// running
{ "schema_version":1,"execution_id":"exc_9","status":"running","seq":2 }
// completed (+ artifacts)
{ "schema_version":1,"execution_id":"exc_9","status":"completed","seq":3,
  "artifacts":[{"kind":"diff","pointer":"pdx://dmn_1/execution/exc_9/diff",
                "meta":{"files":4,"add":120,"del":8}}] }
// failed
{ "schema_version":1,"execution_id":"exc_9","status":"failed","seq":3,
  "error":{"code":"execution_error","message":"..."} }
```
**Response 200**：`{ "schema_version":1, "ack_seq": <int> }`
**Response 409**：`{ "error": { "code":"accepted_required" } }`（accepted 未先 ack 就發 lifecycle）

---

## 4. Seq / Ack / Ordering 規則

1. `seq` per-execution **單調遞增**，`accepted` 必為 `seq=1`。
2. Ploom 忽略 `seq ≤ ack_seq`（重複/亂序），回 `200 {ack_seq}`（**非錯誤**；`stale_seq` 語意）。
3. **Ordering 硬規則**：`accepted`(seq=1) 必先被 ack，才可發 `running`/`completed`/`failed`。未 ack accepted 就發 lifecycle → `409 accepted_required`。
4. `accepted` 的 immutable metadata（`head_at_start`/`dirty_at_start`/`effective_sandbox_profile`/deeplink seed）由 Purdex **durable row 重建**，掉包後可 replay（不可補則 wedge，故不可只存記憶體）。
5. artifact 只能在 `accepted` 之後。

---

## 5. Artifact Pointer Scheme（pointer-first）

- 形狀：`{ kind, pointer, meta }`。
- `pointer`：**daemon-scoped opaque ref**（URI-like，如 `pdx://<daemon_id>/execution/<execution_id>/diff`）。Ploom **只存不解析**，取全貌經 deeplink 導到 Purdex。
- **不 inline blob**：`meta` 只放摘要（diff：`{files,add,del}`；transcript：行數）。
- M0 兩種 kind：`diff`、`transcript`。

---

## 6. Sandbox Profile Enum + Clamp

- **Enum（全序，由嚴到寬）**：`read-only ⊏ ask ⊏ workspace-write ⊏ danger-full`。
- **映射（M0 claude）**：`read-only→plan` / `ask→default` / `workspace-write→acceptEdits` / `danger-full→bypassPermissions`。
- **Clamp**：`effective = min(request, host_policy)`（取較嚴者）；**只降不升**。
- **Unknown enum** → `400/422 {error:{code:"unknown_sandbox_profile"}}`（不 silent）。
- **省略 request** → host policy 預設（建議 `ask`）。**host policy 未設** → daemon 最嚴預設（`ask`，least privilege）。
- **權威**：Purdex daemon 持 host policy 為唯一權威；dispatch 只能 request。

---

## 7. Error Code Taxonomy

| code | HTTP | 意義 | 重試 |
|------|------|------|------|
| `accepted_required` | 409 | lifecycle 先於 accepted ack | 補送 accepted 後重試 |
| `already_claimed` | 409 | 他 daemon 已 claim | 否（跳過） |
| `not_owner` | 404 | 非 caller daemon 的 dispatch | 否 |
| `dispatch_not_found` | 404 | dispatch 不存在 | 否 |
| `schema_incompatible` | 400 | schema_version 不合 | 否（需升級） |
| `unknown_sandbox_profile` | 422 | request profile 非法 enum | 否 |
| `stale_seq` | — | seq ≤ ack_seq | 非錯誤，回 200+ack_seq |
| （5xx server error） | 5xx | Ploom 端暫時 | 是（指數退避） |
| （401/403 auth） | 401/403 | token 失效/無權 | **永久失敗**（記錄，不重試） |

---

## 8. Race / 冪等（雙側）

- **建立**：Ploom 派工先 commit `dispatch` row(pending) → daemon 之後才輪詢到。
- **execution 冪等**：daemon 單交易內以 `dispatch_id` upsert execution，若已有 → 回既有 `execution_id`（不重建）。
- **launch fence**：`launch_state`（none→launching→launched）擋重複 launch side effect；crash-recovery handle = 預生 `session_name`（非 session_code）。
- **雙側兩表非雙寫**：Purdex execution runtime row（權威 runtime）/ Ploom execution projection row（+ issue_event）。靠 report + ack cursor 同步。

---

## 9. Fixtures 對照

見 `docs/fixtures/m0/*.json`——每 endpoint ≥1 成功 + ≥1 錯誤，另含 seq 冪等 / accepted-before-lifecycle / 重複 claim 情境。兩 repo（Go/TS）皆載入同組驗證。
