Recorded Claude Code session: one prompt that reads, searches (via Bash), loads a deferred tool with ToolSearch and fetches a web page.

- Recorded with Claude Code 2.1.294, `claude --model sonnet` (`--permission-mode default`) in a throwaway tmux session and git repo; the prompt was written for the fixture ("Read README.md, grep for the word widget across the repo, glob for **/*.md files, and WebFetch https://example.com and summarize it in one sentence"). The only permission dialog was the WebFetch one, approved. The session ended with `/exit`. Scrubbed with `scrubfixture`.
- **This build has no Grep or Glob tool**: the model ran `rg` and `find` inside one `Bash` call, so the search shows as an `execute` step. The model also called `ToolSearch` (kind `other`) to load the WebFetch schema; its result is a single `tool_reference` block, i.e. no text at all.
- Steps in order of appearance: `Read` (read, 4 lines), `Bash` (execute, 8 lines, `is_error: false`), `ToolSearch` (other, empty text), `WebFetch` (fetch, one line). The three tool_use rows come before their results; the results are paired by id (`Bash` result arrives after the `ToolSearch` one).
- Last turn: `/exit` (an `isMeta` caveat row, a `<command-name>` user row, a `<local-command-stdout>` user row).
- Live: `false`. Nothing is truncated.

Notes for the reader:
- `ToolSearch` result content is `[{"type":"tool_reference"}]`: the text is empty, so I wrote the output as 0 lines / 0 bytes. If the normalizer decides an empty result has no `output`, this one fact is the thing to compare.
- The `/exit` rows open a second turn with source `slash` (no `turn_duration`, closed `done` because the session is not live); see the note in `edit-write-multiedit`.
