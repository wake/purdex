Recorded Claude Code session: Write, Edit and a two-Edit rename (no MultiEdit in this build), with Read before an overwrite.

- Recorded with Claude Code 2.1.294, `claude --model sonnet` (low effort, `--permission-mode acceptEdits`) in a throwaway tmux session and a throwaway git repo (files `app.py`, `notes.md`); the four prompts were written for the fixture. The session ended with `/exit`. Scrubbed with `scrubfixture` (see `testdata/conversation/v1/README.md`).
- Turn 1: `Write` creates a new `hello.py` (kind `edit`, result carries an empty `structuredPatch`).
- Turn 2: `Edit` changes `hello.py` (result carries a `structuredPatch`).
- Turn 3: the prompt asks for one `MultiEdit` call. **This build has no MultiEdit tool**, so the model used two `Edit` calls on `app.py` (rename a function, then a variable) after a `Read`; there is no `MultiEdit` row in the file. Two `thinking` rows (one empty) precede the edits.
- Turn 4: `Read` of an existing `notes.md` then `Write` over it (a `structuredPatch` that removes the old lines).
- Last turn: `/exit` written as `<command-name>/exit` + `<local-command-stdout>` user rows after an `isMeta` caveat row.
- Every step succeeds (no `is_error`); all outputs are far below the 16 KiB cap, so nothing is truncated.
- Live: `false` (the session ended).

Notes for the reader:
- The `/exit` rows are plain `user` rows (not `system/local_command`) and the prompt rows carry no `turnPosition`. I read them by the spec's turn rule 1 (older rows): the `<command-name>` user row opens a fifth turn with source `slash`; the `isMeta` caveat before it is skipped; the `<local-command-stdout>` row after it belongs to that turn. That turn has no `turn_duration`, and is closed `done` only because the session is not live.
