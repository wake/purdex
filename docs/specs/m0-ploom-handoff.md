# Ploom-M0 Handoff Brief

> **給根在 `~/Workspace/wake/ploom` 的獨立 Claude session 開場用。** 本 session（Purdex 側）平行推 PR-Purdex-M0；兩邊靠共享契約解耦。

## 你要做的：PR-Ploom-M0（7 task）

在 Ploom repo（0.1.29-dev）實作派工整合的 Ploom 側。**先讀這三份**（在 Purdex repo，已 merge main）：
- 契約 SOT：`~/Workspace/wake/purdex/docs/specs/m0-contract.md`
- Golden fixtures：`~/Workspace/wake/purdex/docs/fixtures/m0/*.json`（16 檔）
- Plan（Ploom 段）：`~/Workspace/wake/purdex/docs/specs/2026-07-19-m0-dispatch-plan.md` §PR-Ploom-M0

## 契約/fixtures 共享方式（拍板）

**Ploom 端 vendor 一份複本**：把 `m0-contract.md` + `docs/fixtures/m0/` 複製進 Ploom repo（如 `ploom/docs/fixtures/m0/`），header 註明 `synced from purdex@<PR-0 merge commit>`。加一個小 sync-check（或 README 註記）。M0 手動同步；兩 repo 皆本機。Ploom 的 golden-fixture loader test 對這份複本跑。

## 關鍵設計（別重推，已定案）

- **Pull 模型**：Ploom 純 server，daemon 輪詢。你做 4 個 `/daemon/*` 端點被動回應（見契約 E1-E4）。**無** Ploom→Purdex callback。
- **SOT 分工**：Ploom = **projection SOT**，只投影 daemon 回報的 runtime 狀態、**不自行推進** runtime 狀態機。issue 層「關單/done」是人工 gate。
- **五層**：issue→dispatch→execution→attempt(M0=1)→session。**穩定 handle = `execution_id`**（非 session_code）。
- **authz**：daemon 綁 is_agent 帳號複用 S4 token（零新認證）；dispatch 錨點 = issue project **editor+**；E1/E3 隔離**只回 caller daemon 自己的**。
- **雙側兩表非雙寫**：Ploom 存 execution **projection** row（+ issue_event append-only）；Purdex 存 runtime row。靠 report + `ack_seq` 同步。
- **race**：Ploom 派工**先 commit dispatch row(pending)** 再無他事。
- **report 語意**：`seq` 冪等、回 `ack_seq`；**accepted(seq=1) 必先 ack 才收 lifecycle**（否則 `409 accepted_required`）；`stale_seq`(≤ack) 回 200+ack_seq 非錯誤；fail-closed `dispatch_not_found`（不區分不存在/非本 daemon）。

## Task（plan §PR-Ploom-M0，依序）

- **L.0** 更新 Ploom `docs/specs/2026-06-30-s6-purdex-dispatch-design.md`（PARKED）疊五層：加 execution projection 層 + execution_id 取代 session_code + manual reclaim 拉進 M0，對齊本契約。（doc，先做）
- **L.1** dispatch 表 + execution projection 表（migration，OCC 慣例）。
- **L.2** `/daemon/*` E1 poll / E2 claim / E3 fetch（authz 隔離、claim 原子+重複防護、兩段式）。
- **L.3** E4 report（seq 去重 + ack_seq + accepted-before-lifecycle 409 + error taxonomy + 投影寫 issue_event + **組/存 deeplink** `purdex://execution/<id>`，daemon.host 為 optional hint）。
- **L.4** issue 派工 intent（建 pending dispatch，authz editor+）。
- **L.5** `web/src/routes/IssueEditor.tsx` 派工按鈕 + execution 狀態列（execution→issue_event→IssueActivity 時間軸，TanStack Query）。

## 流程慣例（同全域 CLAUDE.md）

- spec review 可省略/最多一輪 → plan → **subagent TDD** → PR → codex review → merge。
- 不直推 main、走 PR + codex review。每 task 獨立 commit。
- codex 派發用 heredoc `<<'EOF'` 包 prompt（反引號會被 zsh 命令替換）；`--model gpt-5.4` 必帶。

## 協調點

- **merge 序固定 PR-0(已merge)→Ploom→Purdex**；build 可與 Purdex 平行。
- Ploom 端可用**假 daemon**（對 golden fixtures）獨立測，不需等 Purdex。
- 契約若要改：改 Purdex 的 SOT → 同步 Ploom vendor 複本 → 兩側重跑。
