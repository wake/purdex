# OpenCode 1.14.23 Fixture Provenance

This document records the provenance of each payload fixture in
`opencode-1.14.23-payloads/`. Per plan v1.3 §2.4, every fixture is
classified as `runtime trace` or `source-derived schema` so reviewers can
audit whether the shape matches what the actual `opencode` runtime emits.

- **Source repo**: `github.com/sst/opencode` @ tag `v1.14.23` (commit `3d31ae2`)
- **Audit report**: [`docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`](../../../../docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md)
- **Catalog**: [`opencode-1.14.23-events.json`](opencode-1.14.23-events.json) — per-event `definedAt` + `payloadFields` source-of-truth
- **Manifest**: [`opencode-1.14.23-manifest.json`](opencode-1.14.23-manifest.json) — points at this directory via `payloadFixtureDir`

## Provenance classification

| Tag | Meaning |
|-----|---------|
| `runtime-trace` | Captured from a real `opencode` runtime by stdout/log scrape — exact bytes the SDK published |
| `source-derived` | Hand-built from the upstream Schema definition (`Schema.Struct`) + `BusEvent.define` callsites — minimum surface that satisfies the schema and the plugin's read paths |

## Fixture-by-fixture

All nine fixtures below are **`source-derived`** for the 1.14.23 audit
pass. No runtime sample was captured because (a) running `opencode` from
within Purdex's CI is out of scope, and (b) the audit (§1 + §2) already
enumerates every read path in `plugin_template.go` and every payload
field in `events.json`, so the source-derived shape is a complete cover
for the OC1 / OC1a contracts.

When a future runtime trace becomes available, swap a fixture in place
and flip its row to `runtime-trace` — OC1 / OC1a will keep the existing
contract green if the runtime payload is a superset of source-derived.

### Bus events

| Fixture | Class | events.json entry | Plugin read paths verified |
|---------|-------|-------------------|----------------------------|
| `session.created.json` | source-derived | `busEvents[upstreamKey=session.created]` (definedAt `session/session.ts:255`, SyncEvent) | `properties.sessionID` |
| `permission.asked.json` | source-derived | `busEvents[upstreamKey=permission.asked]` (definedAt `permission/index.ts:76`, schema `Request`) | `properties.permission`, `properties.patterns` |
| `question.asked.json` | source-derived | `busEvents[upstreamKey=question.asked]` (definedAt `question/index.ts:97`, schema `QuestionRequest`) | `properties.questions` |
| `session.error.json` | source-derived | `busEvents[upstreamKey=session.error]` (definedAt `session/session.ts:281`) | `properties.sessionID`, `properties.error.name`, `properties.error.data.message` |
| `session.status.json` | source-derived | `busEvents[upstreamKey=session.status]` (definedAt `session/status.ts:29`, schema `{sessionID, status: idle | retry | busy}` — fixture uses idle variant per Decision 3 switch / Decision 4 defer for busy/retry) | `properties.sessionID`, `properties.status.type` |
| `session.deleted.json` | source-derived | `busEvents[upstreamKey=session.deleted]` (definedAt `session/session.ts:268`, SyncEvent) | `properties.sessionID` |

### Strong hooks

| Fixture | Class | Hook signature source | Plugin read paths verified |
|---------|-------|-----------------------|----------------------------|
| `chat.message.json` | source-derived | `plugin/index.ts:233-242` (input + output shape per audit §1.6) | `input.sessionID`, `input.messageID`, `input.agent`, `input.model.providerID`, `input.model.modelID`, `output.message.id`, `output.message.agent` |
| `tool.execute.before.json` | source-derived | `plugin/index.ts:265-268` (input + output per audit §1.11) | `input.tool`, `input.callID`, `input.sessionID`, `output.args.subagent_type`, `output.args.description`, `output.args.prompt` |
| `tool.execute.after.json` | source-derived | `plugin/index.ts:273-280` (input + output per audit §1.13) | `input.tool`, `input.callID`, `input.sessionID`, `output.title`, `output.output` |

## Capture script for future runtime traces

When upgrading audit to a new tag and capturing real samples, prefer:

```sh
# Run a real opencode session, intercept Bun-spawn stdin to pdx hook, and
# emit JSON envelopes matching the fixture shape used by OC1 / OC1a.
# Out of scope for the 1.14.23 audit — see plan v1.3 §1.2 (PR-4a-0 not
# in scope: runtime sample capture infrastructure).
```

## Schema deviation policy

- A fixture's `eventType` (Bus) or `hookName` (strong hook) must exactly match the upstream key it represents — OC1a fails the test otherwise.
- Extra fields in fixtures are allowed (audit-quality realism) — OC1a only asserts a documented minimum surface.
- Missing required fields fail OC1a — that is the contract.
- The fixture envelope wraps the payload in `{kind, eventType|hookName, properties|input+output}`; the wrapper itself is a Purdex test convention, not an upstream shape.
