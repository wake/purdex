Synthetic, built from the M-U1-7 shapes: step outputs over the 16 KiB cap, a Read keeping its head and a Bash keeping its tail.

- Hand-written rows (no recording); run through `scrubfixture` for the canonical form.
- Turn 1: a Read result of 400 lines / 21,999 bytes → `truncated`, `keep: head`.
- Turn 2: a Bash result of 1,200 lines `output line NNNNN` / 21,599 bytes → `truncated`, `keep: tail` (the end of the output is what a person wants from a command). Totals in `facts.json` are computed from how the text was generated, not read from the normalizer.
- Closed session (`live: false`).
