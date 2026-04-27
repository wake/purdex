#!/usr/bin/env bash
# Usage: scripts/check-pr-4a-1-boundary.sh <base-ref>
# Exit 0 if all changed files since <base-ref> are within the PR-4a-1
# allowed-path set; non-zero otherwise.
#
# Plan v2.1 §5 + ship gate G2 enforcement. Run locally before opening
# PR-4a-1 and again before merge — the reviewer checklist mirrors this
# script. Like PR-4a-0, this PR explicitly does NOT introduce a CI job.
#
# Allowed-path notes (entries beyond plan v2.1 §5 enumeration):
#   - shell_prompt_test.go: callsites updated for the looksLikeShellPrompt
#     → LooksLikeShellPrompt / stripANSI → StripANSI exports (Slice 2);
#     plan §5 only listed shell_prompt.go but the same-package test must
#     compile against the new names.
#   - export_test.go: minimal test seam in package probe exposing
#     SetWatchPollIntervalForTest; written-first for PR1-PR6 timing.
#   - probe_profile_test.go (cc): characterization test sits next to the
#     impl per Go convention; plan §5 listed only probe_profile.go.
#   - probe_orchestrator_integration_test.go: implementer split CC2b /
#     CC4 / CC5 / CC6 from the unit-style probe_orchestrator_test.go to
#     keep the unit file focused on profile + dedup logic; plan §5
#     listed only probe_orchestrator_test.go.
#   - sweep_test.go: Commit 2 documented deviation — legacy callsite
#     `m.prober.StartWatch(target, func(string, probe.ActivitySignal) {})`
#     migrated to the new Watch API; touching it is unavoidable when
#     ActivitySignal is removed.
#
# Diff form: three-dot ($BASE...HEAD), per PR-4a-0 lessons — two-dot
# would surface false-positive "violations" on files this PR never
# touched whenever main moves while the branch is open.
set -euo pipefail

BASE="${1:-origin/main}"

ALLOWED=(
  'docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan\.md'
  'internal/tmux/executor\.go'
  'internal/tmux/executor_test\.go'
  'internal/tmux/fake_executor\.go'
  'internal/agent/probe/activity\.go'
  'internal/agent/probe/activity_test\.go'
  'internal/agent/probe/probe\.go'
  'internal/agent/probe/shell_prompt\.go'
  'internal/agent/probe/shell_prompt_test\.go'
  'internal/agent/probe/export_test\.go'
  'internal/agent/provider\.go'
  'internal/agent/cc/probe_profile\.go'
  'internal/agent/cc/probe_profile_test\.go'
  'internal/agent/cc/provider\.go'
  'internal/agent/metrics\.go'
  'internal/module/agent/probe_orchestrator\.go'
  'internal/module/agent/probe_orchestrator_test\.go'
  'internal/module/agent/probe_orchestrator_integration_test\.go'
  'internal/module/agent/module\.go'
  'internal/module/agent/module_test\.go'
  'internal/module/agent/handler\.go'
  'internal/module/agent/handler_test\.go'
  'internal/module/agent/sweep_test\.go'
  'scripts/check-pr-4a-1-boundary\.sh'
)

PATTERN="^($(IFS='|'; echo "${ALLOWED[*]}"))$"

VIOLATIONS=$(git diff --name-only "$BASE...HEAD" | grep -vE "$PATTERN" || true)

if [[ -n "$VIOLATIONS" ]]; then
  echo "PR-4a-1 boundary violation — files outside allowed paths:" >&2
  echo "$VIOLATIONS" >&2
  exit 1
fi

echo "PR-4a-1 boundary check passed."
