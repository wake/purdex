# Worker Pane Theme — Phase E (Nexen features) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Pin Nexen v0.15.0, send image attachments natively (non-images keep the phase D path reference), render them on replay, and use Nexen's `session_title` as the worker tab title's first source.

**Architecture:** Bump the pin only on the Go side: `nexen.Assemble` already wires the blob store at `<dataDir>/nex/attachments`, so Purdex adds no `Deps`. On the SPA side:
- Typed capabilities, plus two selectors that gate every new behaviour: images and `session_title`.
- Image chips keep their `File` instead of uploading to the cwd, and are sent as base64 `attachments` on `POST …/messages`.
- The reducer keeps `attachments` metadata on the user message, and the room and chat user lines render thumbnails fetched from the Nexen attachment endpoint.

**Spec:** `docs/specs/2026-09-28-worker-pane-theme-spec.md` §9.2 and §8.4 (title source 1). Nexen contract: `~/Workspace/wake/nexen/docs/contract/capability-matrix.md` §0, §1.9, §1.10, §3, §3.5; `consumer-guide.md` §9.5, §9.6.

## Global Constraints

- Same toolchain and commit rules as the phase A–D plan (`docs/plans/2026-09-28-worker-pane-theme-plan.md` Global Constraints): pnpm; `cd <worktree> && ` prefix; `git commit --only`; `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; a mutation check in every commit body; PRs of ≤ 800 lines or ≤ 20 files.
- **Fail-closed feature detection** (contract §0):
  - Images are sent only when `capabilities.send.attachments.image` exists **and** its `providers` includes the execution's `provider`.
  - The title is used only when the top-level `capabilities.session_title` exists.
  - Older daemons keep the phase D behaviour exactly.
- **Image limits come from the capability, never hard-coded:**
  - `media_types`, `max_bytes` (decoded), `max_count`, `max_total_bytes`
  - `send.max_request_bytes` (the whole JSON body, base64 inflates by 4/3)
  - An image that does not fit falls back to path upload, with the chip note `worker.upload.image_as_path`. It is never dropped silently.
  - The client never sends a request whose serialized body exceeds `max_request_bytes`. If the final text makes the planned set too large at send time, the send is blocked with the visible reason `worker.upload.request_too_large` (remove an image or shorten the text). It does not go out to get a 413.
- **Encoding:** `data` is standard base64 with padding, with no `data:` prefix and no URL-safe alphabet.
- **Title:** `session_title.text` is inserted as plain text (React text node), never as HTML.
- An image-only message (empty text, ≥ 1 image) is valid on send and must render in both views.

## Review Focus

1. **An older daemon (v0.13.2 capabilities)** must behave exactly like alpha.472: images go by path, and the title uses the fallback. Pinned in E1 and E3.
2. **A codex execution on a host that accepts images for claude only** must send images by path, not fail with 400 `attachments_unsupported`. Pinned in E3.
3. **Six 4 MiB images** (24 MiB, over the 20 MiB total cap): the images that fit go native and the rest fall back to path with a note. The send never fails with 400 `attachments_too_large`. Text typed after the images were added is re-planned at send; if it pushes the body over `max_request_bytes`, the send is blocked with a reason instead of a 413. Pinned in E3.
4. **An image-only send** renders a line with thumbnails in room and chat, and does not vanish. Pinned in E4.
5. **A thumbnail fetch that 404s** (`attachment_not_found`) shows a broken-image placeholder with the file's media type, not an empty box. Pinned in E4.

---

## PR E-1 — pin, capabilities, session title

### Task E1: Pin Nexen v0.15.0; capability types and selectors

**Files:**
- Modify: `go.mod` / `go.sum` (`go get lab.protype.tw/wake/nexen@v0.15.0 && go mod tidy`)
- Modify: `spa/src/lib/nex/types.ts` (the `NexCapabilities` `send` object at ~156, `delegate` at ~166, a new top-level `session_title`; `ExecutionSummary.session_title?: { text: string; source: 'custom' | 'agent_name' | 'ai' | string }`)
- Modify: `spa/src/stores/useNexHostStore.ts` (selectors next to `canHandoff`, ~37-60)
- Test: `spa/src/stores/useNexHostStore.test.ts`; `go test ./internal/module/nex/...`

**Interfaces:**
- `interface ImageAttachmentCaps { media_types: string[]; max_bytes: number; max_count: number; max_total_bytes: number; providers: string[]; fetch: { method: string; path: string } }`
- `selectImageAttachments(hostId: string, provider: string): (ImageAttachmentCaps & { maxRequestBytes: number | null }) | null`: returns null unless the image capability exists and `providers` includes `provider`. Numbers are validated (finite and > 0), otherwise null (fail-closed).
- `selectSessionTitleSupported(hostId: string): boolean`: true when `capabilities.session_title` is a non-null object.

- [ ] Step 1: failing tests for the selectors:
  - v0.13.2-shaped caps → null / false
  - v0.15.0 caps with provider `claude` → object; with `codex` → null
  - malformed numbers → null
  - `session_title` present → true
- [ ] Step 2: bump the pin. Run `go build ./... && go test ./internal/module/nex/... ./cmd/...`. Before and after, confirm that `nexen.Assemble` still compiles with Purdex's `nexen.Options` (`internal/module/nex/build_config.go:100-105`). If an options field was renamed, adapt it minimally and note it in the commit.
- [ ] Step 3: implement the types and selectors, then pass. Mutation: drop the providers check and confirm the codex test fails.
- [ ] Step 4: commit `feat: pin Nexen v0.15.0 and read its image and session-title capabilities`.

### Task E2: session_title as the first title source

**Files:** `spa/src/lib/nex/worker-summary.ts:36-40` (`workerTitleOf`: widen the summary pick to include `session_title` and take a `titleSupported: boolean`), `spa/src/hooks/useTabDisplay.ts:~104`, `spa/src/hooks/useNotificationDispatcher.ts:~341`, tests.

- `workerTitleOf(content, summary, titleSupported)` passes `sessionTitle: titleSupported ? summary?.session_title?.text : undefined` to `workerTabTitle`.
- Both callers read `selectSessionTitleSupported(host)`.

- [ ] Step 1: failing tests:
  - With the capability and `session_title.text = 'Fix login'` → tab `Fix login - repo`.
  - Without the capability, the same summary → fallback title.
  - The notification title equals the tab title in both cases (extend the existing parity test).
  - A title containing `<b>` renders literally.
- [ ] Step 2: implement, then pass. Mutation: ignore `titleSupported` and confirm the "without capability" test fails.
- [ ] Step 3: commit `feat(spa): worker tab title prefers Nexen session_title`.

(The site-level SSE opens `/api/nex/v1/events` with no `kind=` filter (`lib/nex/execution-list-effects.ts:165-176`), so `execution.title_changed` already triggers the debounced list refetch. No production change is needed. E2 adds one case to `spa/src/lib/nex/execution-list-effects.test.ts` (list it in E2's Files): a site-level `execution.title_changed` frame, with its payload stripped, schedules a list refetch.)

---

## PR E-2 — native images

### Task E3: Image chips send as native attachments

**Files:**
- `spa/src/lib/nex/worker-upload.ts`: `Chip` gains `kind: 'path' | 'image'`; a pure planner.
- `spa/src/hooks/useWorkerUploads.ts`: keep `File`s for image chips in a ref map; skip the cwd upload for chips planned as `image`.
- `spa/src/lib/nex/nex-api.ts:203`: `sendMessage(hostId, executionId, leaseId, text, attachments?: WireImageAttachment[])`.
- `spa/src/hooks/useExecutionActions.ts`: `handleSend(text, opts?: SendOptions & { attachments?: WireImageAttachment[] })`. It forwards attachments, and `pendingLocal` gains `attachments?: { previewUrl: string; media_type: string }[]` for the optimistic line.
- `spa/src/components/execution/ExecutionView.tsx`: `sendWithAttachments` encodes and passes the images.
- `spa/src/lib/nex/types.ts` (~265 `NexApiError`, ~288 `nexErrorFromResponse`): add `attachmentIndex?: number`, parsed from the body's `attachment_index` (a non-negative integer, otherwise undefined). Map the six attachment codes plus `request_too_large` in the chip and upload error map (`lib/nex/worker-upload.ts`). A per-image error marks the native chip at that index (in send order) as failed.
- `spa/src/locales/en.json` and `zh-TW.json`: `worker.upload.image_as_path`, `worker.upload.request_too_large`, and one message per new error code. The locale completeness test covers them.

**Interfaces:**
- `type WireImageAttachment = { type: 'image'; media_type: string; data: string }`
- `planAttachments(files: { key: string; size: number; type: string }[], caps: ReturnType<typeof selectImageAttachments>, text: string): { native: string[]; path: string[] }` is pure. It walks files in order. A file goes native only when all of these hold: caps are non-null; its type is in `media_types`; `size <= max_bytes`; the native count is still `< max_count`; the running native total plus its size is `<= max_total_bytes`; and `requestBytes(text, nativeSoFar + this) <= maxRequestBytes` (when that is known). Anything else goes to `path`.
- `requestBytes(text: string, images: { size: number; type: string }[], leaseIdLength = 64): number` is exact and pure. It is the UTF-8 byte length of `JSON.stringify({ lease_id: 'x'.repeat(leaseIdLength), text, attachments: images.map(i => ({ type: 'image', media_type: i.type, data: '' })) })`, plus `Σ 4*ceil(size/3)` for the base64 payloads. Test it against the real serialized body for boundary sizes (size mod 3 = 0/1/2), text containing quotes, backslashes, newlines and CJK, and an exact-limit case.
- `encodeImage(file: File): Promise<string>` returns standard base64 via `FileReader.readAsDataURL`, stripping the `data:*;base64,` prefix.
- When an image chip is added, the planner runs on the current set with the current draft text. Images planned `path` upload immediately, as in phase D, with the note `worker.upload.image_as_path`. Images planned `native` stay local (`status: 'done'`, no `path`). Once a chip is uploaded as path, it stays path.
- **At send**, `requestBytes(finalText, nativeImages)` is checked once more. If it is over `maxRequestBytes`, the send is blocked with `worker.upload.request_too_large` (visible reason, `role=status`); nothing is posted, and the chips and draft stay.
- **Optimistic previews own their URLs.** At send, create fresh object URLs from the native `File`s for `pendingLocal.attachments`. Do not reuse the chips' `previewUrl`s: `uploads.clear()` revokes those as soon as the send succeeds. ExecutionView revokes the pending URLs when `pendingLocal` changes or clears, and on unmount (an effect keyed on the pendingLocal identity).
- On send, native images are encoded and passed as `attachments`, and path chips become `[file:]` lines as before. Text may be empty when at least one native image exists (contract §1.9). `canSend` still blocks while anything is uploading or failed.

- [ ] Step 1: failing tests:
  - `planAttachments`: all Review Focus lines 1–3 (null caps → all path; codex provider → caller passes null → all path; 6×4 MiB → the first 5 native, the 6th path; an unsupported type → path; a request-size cap).
  - `encodeImage` round trip.
  - `sendMessage` body shape: attachments only when non-empty, and the key is absent otherwise.
  - `requestBytes` equals `new TextEncoder().encode(JSON.stringify(realBody)).length` for the boundary cases above.
  - Text grown after planning so the body exceeds `max_request_bytes` → send blocked with the reason, `sendMessage` not called.
  - Optimistic line: after a successful send clears the chips, the pending line's thumbnails still load (their URLs are not revoked), and they are revoked when `pendingLocal` clears.
  - `nexErrorFromResponse` parses `attachment_index` (0, 3, missing, negative, non-integer).
  - An image-only send posts `text: ""` plus attachments.
  - Errors: `attachment_too_large` with `attachment_index: 1` marks the second native chip failed and keeps the draft.
- [ ] Step 2: implement, then pass. Mutations: planner ignores `providers` (caps not null for codex, then Review Focus 2 fails); the total cap is removed.
- [ ] Step 3: commits, split as the implementer sees fit: the planner and encoder, then the send path.

### Task E4: Replay and render attachment thumbnails

**Files:**
- `spa/src/lib/nex/event-reducer.ts:~236,505-514`: `userBubble(text, attachments?)` stores `attachments: {media_type, bytes, sha256}[]` on the user message as a side field `purdex_attachments` (not a content block, so Claude-shaped consumers are untouched). The message is appended even when the text is empty but attachments exist.
- `spa/src/lib/nex/nex-api.ts`: `fetchAttachment(hostId, executionId, sha256): Promise<Blob>` via `pinnedHostFetch` on the capability's `fetch.path`, with `{id}` and `{sha256}` substituted and URL-encoded. **The path is origin-relative and already includes the public prefix** (`/api/nex/v1/...`, consumer-guide §9.5). Resolve it against the daemon base the same way `pinnedHostFetch` resolves `/api/...` paths, and do **not** go through `nexFetch`, which prepends `/api/nex` again.
- `spa/src/components/room/AttachmentThumbs.tsx` (new): for each item, fetch (bounded concurrency of 4), turn the result into an object URL, and revoke it on unmount. It shows a placeholder with the media type while loading or on error. Clicking opens the full image in a lightweight overlay, or a new tab via the blob URL — use whichever the codebase already has for images; otherwise, a new tab.
- `spa/src/components/room/MessageRow.tsx:176-212` (room), `spa/src/components/chat/ChatTranscript.tsx:106-126` (chat, where the "skip empty text" at ~114 must allow attachment-only), `ExecutionView.tsx:410,414` (optimistic line shows `pendingLocal.attachments` previews).

- [ ] Step 1: failing tests:
  - The reducer keeps `purdex_attachments` and appends an image-only message.
  - Replay from history equals the live result.
  - The room and chat user lines render one thumbnail per attachment.
  - A 404 renders the placeholder (Review Focus 5).
  - Object URLs are revoked on unmount.
  - `fetchAttachment` requests exactly `<daemon base>/api/nex/v1/executions/<encoded id>/attachments/<sha>` with no doubled `/api/nex`, carries the host's auth header, and uses the capability's `fetch.path` template (a test with a different template proves it is read, not hard-coded).
  - The search index is unchanged (thumbnails are not search units).
- [ ] Step 2: implement, then pass. Mutation: skip the message when the text is empty and confirm the image-only test fails.
- [ ] Step 3: commit `feat(spa): show image attachments on worker user lines`.

## Gates and order

E1 → E2 → PR E-1 (review → merge → bump; **daemon redeploy**: the pin changes, and the schema is still v6, so no DB wipe). Then E3 → E4 → PR E-2 (review → merge → bump; SPA only). Before bumping, `git fetch` VERSION each time.
