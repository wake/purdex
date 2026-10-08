Recorded Claude Code session: Read of a PNG, whose tool result is an image block.

- Recorded with Claude Code 2.1.294, `claude --model sonnet` (`--permission-mode acceptEdits`) in a throwaway tmux session and git repo containing a tiny `tiny.png` (a 2x2 RGB PNG made by the recorder); the prompt "Read the file tiny.png and describe it in one sentence." was written for the fixture. The session ended with `/exit`. Scrubbed with `scrubfixture`.
- Turn 1: one `Read` step (kind `read`, status `done`). The tool result's content is a list holding one `image` block (`source.media_type: image/png`) and no text. The output text is therefore the single line `[image]` (7 bytes, 1 line), and `output.images` has one entry `image/png`.
- **Scrubber note**: the scrubber replaces the image base64 by a 1x1 PNG, so the image's `bytes` is the size of that tiny PNG (70 bytes), not of the original file.
- Turn 2: `/exit` (an `isMeta` caveat row, a `<command-name>` user row, a `<local-command-stdout>` user row).
- Live: `false`. Nothing is truncated.

Notes for the reader:
- The `/exit` rows open a second turn with source `slash`, closed `done` only because the session is not live; see the note in `edit-write-multiedit`.
