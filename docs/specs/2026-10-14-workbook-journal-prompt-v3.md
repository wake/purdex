# Workbook journal — summariser prompt v3 (round 2, being measured: J-P)

> prompt_ver 3 (journal spec v4 §9). A **journal job** (a leader's or a solo session's turn) sends the v2 turn prompt (`docs/specs/2026-10-10-session-workbook-prompt-v2.md`, 「Turn prompt」 and 「待辦清單」, unchanged) **followed by the journal block below**; a member's job sends v2 alone. The re-write and refresh prompts are unchanged.
> How the output becomes entries (daemon, spec v4 §5.1): `entry` → one `exec` entry, or `exec_done` when `thing_done` (not both); each `events` item → one `request` / `comm` / `confirm` entry; `task_new` / `task_done` come from the task source, never from the model. `skip: true` drops the exec entry only — events are still written.
> Measurement (J-P): ≥ 30 real turns — 15 leader turns, 15 solo turns — through `claude -p --model haiku` with `effort: low`, the v2 method. Pass bar in §3.

## 1. Journal block (appended to the system prompt of a journal job)

```text
## 日誌（leader 或單獨 session 才有這一段，同一次輸出一併寫）

這份工作簿同時是給主管看的日誌。除了 entry，請把這一輪「別人要求、彼此溝通、要使用者回答」的事，寫成 events。

輸入另外有：
- unit_role："leader"（這個 session 帶一個 team，日誌是整個 team 的）或 "solo"（單獨一個 session）
- self_name：這個 session 自己的名字（events 的 from／to 寫到自己時用它）
- open_confirms：之前留給使用者、還沒回答的問題，每筆 {n, asked, idx, text}：n 是編號；asked 是第幾次提問（數字越大越新）；idx 是那次提問裡的第幾題
- roadmap_rows：這個專案的路線圖，每筆 {feature, stage, desc, status}

events 每一則是下面三種之一：
- request：有人要求某人做某件事或做決定。by 填 "user"（使用者要求）、"leader"（leader 要求 member）或 "member"（member 要求 leader，例如請 leader 裁定一個問題）；from、to 填名字或「使用者」。
  例：使用者要 88 查清楚「解除沒有反應」→ {"kind":"request","by":"user","from":"使用者","to":"88","text":"使用者要求查清楚 member 的逾時暫停按解除沒有反應。"}
- comm：通知、回報、知會，不是在要求什麼，也不需要對方回覆。from、to 同上。
  例：1f 通知 734 已部署 → {"kind":"comm","from":"1f","to":"88","text":"1f 通知 alpha.734 已部署到兩台，可以請使用者真機驗。"}
- confirm：這個 session 這一輪**最後的回覆**裡，留給使用者回答或決定、到這一輪結束都還沒得到回答的問題。一則訊息只寫一則 confirm。
  - items：**回覆裡每一個要使用者回答或決定的問題都要有一個 item**——編號題、標了「最優先」的題、附「我的提議」要使用者同意的題、最後補問的小問題都算；每個 ≤ 30 字，照提問順序。**寫完回頭數一次：item 數要和回覆裡的問題數一樣。**
  - detail_start：照抄提問段落**開頭的 10～20 個字**，一字不差（系統會從這裡截到回覆結尾當作原文，所以不要改寫）。
  例：回覆最後是「要你決定：1. 日誌檔要不要進 git？2. 按鈕上要不要數字？另外，篩選要不要加一類？」→ {"kind":"confirm","from":"88","to":"使用者","text":"日誌規格還有三件事要使用者決定。","items":["日誌檔要不要進 git","按鈕上要不要數字","篩選要不要加一類"],"detail_start":"要你決定：1. 日誌檔要不要進 git"}

寫 events 的規則：
- 只寫這一輪發生的事，舊的不要重寫。每輪 0 到 4 則；沒有就給空陣列。
- **turn.user_prompt 開頭的 `[甲 → 乙]`、`[report …]`、`[pdx team] …` 告訴你是誰傳來的**：用那個名字當 from。`[pdx team]` 開頭的是系統通知，不要寫成 events。
- 要求做事、做決定，或要求回覆、確認、提供資訊（「請告訴我」「有沒有…」「可以嗎」）→ request；只是告知 → comm。這個 session 自己在 assistant_text 裡要求別人做事（例如派工、請 member 改一個地方）也是 request（by "leader"）。
- assistant_text 裡預告「接下來要做」的事，還沒發生，不要寫成 events。
- text 一句話、≤ 60 字，主詞寫清楚（誰對誰、要什麼或說什麼）。不用「你」「我」。
- **不要寫任務建立或任務完成**，也不要自己編任務編號：任務由系統另外記錄。entry 裡可以提到派了什麼工、哪件事做完。
- 使用者在選項按鈕裡已經回答的問題，不是 confirm。只是問「要繼續嗎」這類客套、沒有實際要決定的事，也不是 confirm。

confirm_done：逐筆看 open_confirms，**只勾這一則 turn.user_prompt 真的回答到的**，把 n 放進陣列。
- 使用者常用編號回答（「1 可以、2 乙」）：編號指的是**最近一次提問（asked 最大的那組）裡的第幾題（idx）**，不是 n。
- 給了答案、選了選項、說「可以」「ok」，或用自己的話決定、否決、改了做法，都算回答。
- 更早的提問這一則沒提到，就不要勾；拿不準就不勾。
  例：open_confirms 有 {n:7, asked:3, idx:1, "日誌檔要不要進 git"}、{n:8, asked:3, idx:2, "按鈕上要不要數字"}、{n:2, asked:1, idx:1, "姓名池名單"}，使用者說「1 可以，2 先不要」→ confirm_done [7, 8]（n:2 是更早的提問，沒被提到）。

roadmap：**只有這一輪的內容直接點名某個階段（例如「J-1a」「RA-2」），並且說它開始做、送審、merge 完成或暫停**，才寫 {feature, stage, status}；相近的工作、只做了原型或準備工作都不算；拿不準就給空陣列。status 只能是「未開始」「進行中」「審查中」「完成」「暫停」。feature 和 stage 必須照抄 roadmap_rows 裡有的，不要新增或改名。沒有變化就給空陣列。

輸出 JSON 多三個欄位：
"events": [...], "confirm_done": [n, …], "roadmap": [{"feature": "…", "stage": "…", "status": "…"}]
confirm 的欄位是 kind、from、to、text、items、detail_start（沒有 detail）。
```

## 2. Input (journal job; the v2 fields plus)

```
{ …v2 fields…,
  "unit_role": "leader" | "solo",
  "self_name": "purdex-88-b8",
  "open_confirms": [ {"n": 1, "asked": 3, "idx": 1, "text": "日誌檔要不要進 git"} … at most 30, oldest first ],
  "roadmap_rows": [ {"feature": "工作簿日誌", "stage": "J-1", "desc": "daemon：檔案寫入、API、git", "status": "未開始"} … the features this unit works on, at most 40 rows ] }
```

`n` numbers the open items for this job only; `asked` numbers the unit's confirm entries that still have open items (oldest 1), `idx` is the item's 1-based place in its entry; the daemon keeps the `n → (entry id, item index)` map, as for todos. `detail` is cut by the daemon from `turn.assistant_text`, from the first occurrence of `detail_start` to the end (not found → the last 1 500 chars), then scrubbed and capped at 4 000.

## Round log

- Round 1 (2026-10-14, 15 leader + 15 solo turns): classification 13/15 and 15/15; confirm items exact 4/6 (leader) and 7/7 (solo), detail verbatim 3/8 and 6/7; confirm_done wrong in both lanes (leader 15 ticked 5 items of older asks; solo missed answers given as 「可以」 and as a pushback); roadmap set 進行中 for work that was only a prototype; no invented task numbers; entry avg 96 / 95, push ≤ 40, no parse failure; latency p50 14.0 s / 11.6 s (≈ 1.5–1.9 k thinking tokens at effort low; no v2 baseline yet). Round 2 = this text: item count check, `detail_start` cut by the daemon, `asked` / `idx` and the 「only this message」 rule for ticks, stricter roadmap, request covers asks for a reply, no future steps as events, `self_name`.

## 3. Pass bar for J-P (per lane of 15 turns, judged by hand against the transcript)

- `request` / `comm` / `confirm` classification right in ≥ 13 of 15 turns; no `[pdx team]` notice written as an event.
- Every turn whose final reply leaves the person questions yields one `confirm` with the right number of items (± 0 on ≥ 13 of 15 such turns); `detail` is the reply's text, not a rewrite.
- `confirm_done` right on every turn that follows a confirm (no item ticked that was not answered).
- No task number invented in any event; no `task_new` / `task_done` kind produced.
- `entry`, `push`, `todos` within the v2 bars (entry average ≤ 125, push ≤ 40, no parse failure).
- Latency p50 within 1.5× the v2 prompt's (2.6 s API with `effort: low`).

A miss → change the block, re-run the lane; the rounds go in this document's header like v2's.
