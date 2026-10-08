Synthetic, built from the M-U1-7 shapes: a peer message, a local slash command, a slash command that reaches the model, a scheduled wake-up and an absorbed queued prompt.

- Hand-written rows (no recording); run through `scrubfixture` for the canonical form.
- Turn 1: `isMeta` prompt row with `origin.kind: peer` and the `<cross-session-message>` wrapper → `peer`, text = the body, `from.name` = the sender.
- Turn 2: a `system/local_command` pair (`/model` and its `<local-command-stdout>`) → `slash` + a `command_output` item; no model work, `done`.
- Turn 3: a prompt row `<command-name>/review</command-name>…<command-args>src/</command-args>` → `slash` with text `/review src/`; its `isMeta` expansion row is skipped.
- Turn 4: `isMeta` row with `turnOrigin: scheduled`, no `origin` → `scheduled`.
- Turn 5: a typed prompt and an `attachment` `queued_command` inside the running turn → a `queued` user item in the same turn (it opens no turn).
- Closed session (`live: false`).
