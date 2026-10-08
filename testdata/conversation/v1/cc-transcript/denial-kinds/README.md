Synthetic, built from the M-U1-7 shapes: every `toolDenialKind` value with and without `is_error`, a killed-process turn, a failed step and an old-style refusal.

- Hand-written rows (no recording); the combinations (a `permission-rule` denial inside a turn that goes on, `is_error` missing) are the matrix of spec §8.1, not a transcript. Run through `scrubfixture` for the canonical form.
- Turn 1: four Bash steps, `toolDenialKind` = `user-rejected` / `permission-rule` / `interrupted` / `cancelled`, `is_error: true` → `denied` + that value.
- Turn 2: the same four values with `is_error: false` (two) or no `is_error` (two) → still `denied` + the value.
- Turn 3: a Bash `tool_use` with no result and no `turn_duration`; the next prompt closes the turn → `interrupted`, the step is `denied` with `denial: interrupted` (killed process).
- Turn 5: `Exit code 1` with `is_error: true` and no denial field → `failed`.
- Turn 6: a Read refused in older Claude Code style (error text "doesn't want to proceed", no `toolDenialKind`), then the `[Request interrupted by user for tool use]` row and `turn_duration` → step `denied` `user-rejected`, turn `interrupted`.
- Closed session (`live: false`).
