#!/usr/bin/env bash
# Usage: scripts/check-pr-4a0-boundary.sh <base-ref>
# Exit 0 if all changed files since <base-ref> are within the PR-4a-0
# allowed-path set; non-zero otherwise.
#
# Plan v1.3 §5 + ship gate H6.3 enforcement. Run locally before opening
# PR-4a-0 and again before merge — the reviewer checklist mirrors this
# script. PR-4a-0 explicitly does NOT introduce a CI job (Round 3 M3-3);
# if a CI gate is wanted later it ships in its own PR.
#
# Allowed-path note: plan v1.3 §5 enumerates plugin_template_test.go as
# the OC1 / OC1a test location. Commit 4 split the contract tests into
# plugin_template_contract_test.go so the existing state-machine file
# stays focused; this script extends the allowed list by that one entry,
# which is documented in the PR description as a Commit 4 split decision.
#
# spa/src/lib/agent-icons.test.tsx remains in the allowed set even
# though PR #662 already shipped strengthened OI1 / OI2 coverage on main
# (alpha.232) — the entry is kept so any future fix-up to those tests
# inside this PR's review loop is not accidentally blocked.
#
# Diff form: three-dot ($BASE...HEAD), not the two-dot template in plan
# v1.3 §5. Two-dot includes any divergence on $BASE since the merge-base
# (e.g. main shipping alpha-bumps while this branch was open), which
# would surface as false-positive "violations" on files this PR never
# touched. Three-dot scopes the comparison to changes on HEAD's side
# only — the actual question this script asks. The plan template's
# two-dot form is a known correctness gap we patch here.
set -euo pipefail

BASE="${1:-origin/main}"

ALLOWED=(
  'docs/specs/2026-04-26-opencode-1\.14\.23-hook-audit\.md'
  'docs/specs/2026-04-26-lights-rebuild-phase-4a-plan\.md'
  'internal/agent/opencode/events\.go'
  'internal/agent/opencode/events_test\.go'
  'internal/agent/opencode/hooks\.go'
  'internal/agent/opencode/hooks_test\.go'
  'internal/agent/opencode/plugin_template\.go'
  'internal/agent/opencode/plugin_template_test\.go'
  'internal/agent/opencode/plugin_template_contract_test\.go'
  'internal/agent/opencode/testdata/opencode-1\.14\.23-.*'
  'spa/src/lib/agent-icons\.test\.tsx'
  'scripts/check-pr-4a0-boundary\.sh'
)

PATTERN="^($(IFS='|'; echo "${ALLOWED[*]}"))$"

VIOLATIONS=$(git diff --name-only "$BASE...HEAD" | grep -vE "$PATTERN" || true)

if [[ -n "$VIOLATIONS" ]]; then
  echo "PR-4a-0 boundary violation — files outside allowed paths:" >&2
  echo "$VIOLATIONS" >&2
  exit 1
fi

echo "PR-4a-0 boundary check passed."
