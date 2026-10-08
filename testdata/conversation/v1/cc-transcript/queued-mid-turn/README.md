A prompt typed during a running tool that is absorbed into the running turn, and a prompt typed just after the answer that becomes the next turn's `queued` prompt row, recorded from Claude Code 2.1.294.

- Recorded with `claude --model sonnet` (2.1.294, acceptEdits) in a throwaway tmux session and git repo; prompts written for the fixture. Each of two Bash `for` loops (6 × `echo`/`sleep 4`) needed one permission approval. Scrubbed with `scrubfixture`; paths are `/work/…`.
- Turn 1: prompt 1 starts the loop; a second prompt ("After you finish, also reply with the word QUEUEDTWO.") sent while the tool ran is written as `queue-operation enqueue`/`remove` and an `attachment` `queued_command` (origin human, `commandMode: prompt`) inside the same turn → a `queued` user item in turn 1; it opens no turn. Turn 1's first user item is the typed prompt, so `user_source` is `user`. One reply "done one / QUEUEDTWO", one `turn_duration`.
- Turn 2: a second loop prompt, typed; reply "done three".
- Turn 3: a prompt sent right after the answer ("Also reply with the word QUEUEDFOUR…") waited for the turn to end and is a normal user row with `promptSource: queued` → turn with source `queued`.
- Turn 4: `/exit` (slash, with its stdout row).
- Steps: the two Bash loops, `execute`, done, 6-line outputs (41 and 47 bytes, not truncated).
- This build has no MultiEdit / Grep / Glob tools.
- Closed session (`live: false`).

Notes for the reader
- `queue-operation` rows (enqueue / dequeue / remove) are not conversation content; the facts rely on the `queued_command` attachment and the `promptSource: queued` row only.
