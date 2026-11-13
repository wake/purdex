// Package codexbroker provides read-only inventory of codex `app-server-broker.mjs`
// processes, state directories, and unix sockets visible on the host.
//
// This package implements Phase P1 of the codex-broker governance feature.
// See docs/specs/2026-05-01-codex-broker-governance-spec.md for the full design.
//
// P1 is strictly read-only:
//   - No process signalling (no kill / killpg).
//   - No filesystem mutation under ~/.claude/plugins/data/codex-openai-codex/state.
//   - No broker socket connect (no RPC).
//
// The background auto sweep lives in module.go; its rules are in
// docs/specs/2026-11-13-codex-broker-auto-sweep-spec.md.
//
// P2 (decision + kill) and P3 (triggers + caching) are implemented in
// subsequent worktrees referencing the same spec.
package codexbroker
