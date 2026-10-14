# Workbook journal — summariser prompt v3 (draft, to be measured: J-P)

> prompt_ver 3 (journal spec v4 §9). A **journal job** (a leader's or a solo session's turn) sends the v2 turn prompt (`docs/specs/2026-10-10-session-workbook-prompt-v2.md`, 「Turn prompt」 and 「待辦清單」, unchanged) **followed by the journal block below**; a member's job sends v2 alone. The re-write and refresh prompts are unchanged.
> How the output becomes entries (daemon, spec v4 §5.1): `entry` → one `exec` entry, or `exec_done` when `thing_done` (not both); each `events` item → one `request` / `comm` / `confirm` entry; `task_new` / `task_done` come from the task source, never from the model. `skip: true` drops the exec entry only — events are still written.
> Measurement (J-P): ≥ 30 real turns — 15 leader turns, 15 solo turns — through `claude -p --model haiku` with `effort: low`, the v2 method. Pass bar in §3.

## 1. Journal block (appended to the system prompt of a journal job)

```text
## 日誌（leader 或單獨 session 才有這一段，同一次輸出一併寫）

這份工作簿同時是給主管看的日誌。除了 entry，請把這一輪「別人要求、彼此溝通、要使用者回答」的事，寫成 events。

輸入另外有：
- unit_role："leader"（這個 session 帶一個 team，日誌是整個 team 的）或 "solo"（單獨一個 session）
- open_confirms：之前留給使用者、還沒回答的問題，每筆 {n, text}（n 是編號）
- roadmap_rows：這個專案的路線圖，每筆 {feature, stage, desc, status}

events 每一則是下面三種之一：
- request：有人要求某人做某件事或做決定。by 填 "user"（使用者要求）、"leader"（leader 要求 member）或 "member"（member 要求 leader，例如請 leader 裁定一個問題）；from、to 填名字或「使用者」。
  例：使用者要 88 查清楚「解除沒有反應」→ {"kind":"request","by":"user","from":"使用者","to":"88","text":"使用者要求查清楚 member 的逾時暫停按解除沒有反應。"}
- comm：通知、回報、知會，不是在要求什麼。from、to 同上。
  例：1f 通知 734 已部署 → {"kind":"comm","from":"1f","to":"88","text":"1f 通知 alpha.734 已部署到兩台，可以請使用者真機驗。"}
- confirm：這個 session 這一輪**最後的回覆**裡，留給使用者回答或決定、到這一輪結束都還沒得到回答的問題。一則訊息只寫一則 confirm；每個問題一個 item（≤ 30 字，照提問順序）；detail 照抄回覆裡提問的那一段原文（不改寫、不摘要）。
  例：回覆最後問了「1. 日誌檔要不要進 git？2. 按鈕上要不要數字？」→ {"kind":"confirm","from":"88","to":"使用者","text":"日誌規格還有兩件事要使用者決定。","items":["日誌檔要不要進 git","按鈕上要不要數字"],"detail":"1. 日誌檔要不要進 git？…"}

寫 events 的規則：
- 只寫這一輪發生的事，舊的不要重寫。每輪 0 到 4 則；沒有就給空陣列。
- **turn.user_prompt 開頭的 `[甲 → 乙]`、`[report …]`、`[pdx team] …` 告訴你是誰傳來的**：用那個名字當 from。`[pdx team]` 開頭的是系統通知，不要寫成 events。
- 要求做事或做決定 → request；只是告知 → comm。這個 session 自己在 assistant_text 裡要求別人做事（例如派工、請 member 改一個地方）也是 request（by "leader"）。
- text 一句話、≤ 60 字，主詞寫清楚（誰對誰、要什麼或說什麼）。不用「你」「我」。
- **不要寫任務建立或任務完成**，也不要自己編任務編號：任務由系統另外記錄。entry 裡可以提到派了什麼工、哪件事做完。
- 使用者在選項按鈕裡已經回答的問題，不是 confirm。只是問「要繼續嗎」這類客套、沒有實際要決定的事，也不是 confirm。

confirm_done：逐筆看 open_confirms，turn.user_prompt 裡使用者已經回答的（給了答案、選了選項、說「可以」「ok」也算），把 n 放進陣列；沒回答的不放。
  例：open_confirms 有 {n:1,"日誌檔要不要進 git"}、{n:2,"按鈕上要不要數字"}，使用者這一輪說「1 可以，2 先不要」→ confirm_done [1, 2]。

roadmap：這一輪明確顯示 roadmap_rows 裡某個階段的狀態變了（開始做、送審、merge 完成、確定暫停），才寫 {feature, stage, status}；status 只能是「未開始」「進行中」「審查中」「完成」「暫停」。feature 和 stage 必須照抄 roadmap_rows 裡有的，不要新增或改名。沒有變化就給空陣列。

輸出 JSON 多三個欄位：
"events": [...], "confirm_done": [n, …], "roadmap": [{"feature": "…", "stage": "…", "status": "…"}]
```

## 2. Input (journal job; the v2 fields plus)

```
{ …v2 fields…,
  "unit_role": "leader" | "solo",
  "open_confirms": [ {"n": 1, "text": "日誌檔要不要進 git"} … at most 30, oldest first ],
  "roadmap_rows": [ {"feature": "工作簿日誌", "stage": "J-1", "desc": "daemon：檔案寫入、API、git", "status": "未開始"} … the features this unit works on, at most 40 rows ] }
```

`n` numbers the open items for this job only; the daemon keeps the `n → (entry id, item index)` map, as for todos.

## 3. Pass bar for J-P (per lane of 15 turns, judged by hand against the transcript)

- `request` / `comm` / `confirm` classification right in ≥ 13 of 15 turns; no `[pdx team]` notice written as an event.
- Every turn whose final reply leaves the person questions yields one `confirm` with the right number of items (± 0 on ≥ 13 of 15 such turns); `detail` is the reply's text, not a rewrite.
- `confirm_done` right on every turn that follows a confirm (no item ticked that was not answered).
- No task number invented in any event; no `task_new` / `task_done` kind produced.
- `entry`, `push`, `todos` within the v2 bars (entry average ≤ 125, push ≤ 40, no parse failure).
- Latency p50 within 1.5× the v2 prompt's (2.6 s API with `effort: low`).

A miss → change the block, re-run the lane; the rounds go in this document's header like v2's.
