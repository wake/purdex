Synthetic, built from the M-U1-7 shapes: API errors (`<synthetic>` assistant row, `isApiErrorMessage`, `error`), which cannot be provoked on demand.

- Hand-written rows (no recording); CC `version` field 2.1.292 is the shape's version, not a recording. Generated once, then run through `scrubfixture` for the canonical form.
- Turn 1: `rate_limit` error row then `turn_duration` → `failed` with `error {kind: rate_limit}`, no `agent_text` for the error text.
- Turn 2: a plain reply (no error carried over).
- Turn 3: a `server_error` row followed by a real reply in the same turn → the later reply clears the error, the turn is `done`.
- Closed session (`live: false`).
