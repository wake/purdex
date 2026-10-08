# Conversation golden fixtures, v1

Test data for the conversation model of the daemon (`internal/convmodel`, spec
`docs/specs/2026-10-08-interface-u1-spec.md` §8.1). Each case is a Claude Code
transcript (`input.jsonl`), the model the daemon builds from it
(`expected.json`) and a hand-written list of facts about it (`facts.json`).

## For the Apps (iOS, Mac)

Fetch **only** these files, pinned by commit and checked against the sha256 in
`MANIFEST.json`:

| File | What |
|---|---|
| `MANIFEST.json` | the case list: `{"version": 1, "cases": [{name, source, cc_version, description, input, expected, facts, sha256{input, expected, facts}}]}` |
| `cc-transcript/<case>/expected.json` | `{"live": bool, "conversation": <wire form, spec §8.1>}`, pretty-printed, stable key order |
| `cc-transcript/<case>/facts.json` | hand-written checks (format below) |

Decode `expected.json` into your model and compare; use `facts.json` as an
independent check of your own reading of the rules. **Ignore unknown fields**
(`MANIFEST.json` cases may carry a `children` list, `facts.json` may grow).
Times are integer milliseconds since the epoch. `key.host_id` is empty and
`key.session_id` is the fixed fixture id `00000000-0000-4000-8000-000000000001`.

**Do not fetch `input.jsonl`** (or `children/`): it is the daemon's own test
input, scrubbed but still a transcript, and the Apps have no use for it. The
copy script should take `MANIFEST.json`, `expected.json` and `facts.json` of
every case and nothing else; `mod-events/<case>/` is reserved for U1-5.

## Layout

```
MANIFEST.json
README.md
cc-transcript/<case>/input.jsonl       scrubbed transcript (daemon test input only)
cc-transcript/<case>/expected.json     normalizer output; regenerated with -update
cc-transcript/<case>/facts.json        hand-written; never regenerated
cc-transcript/<case>/README.md         where it came from and what it covers (line 1 = MANIFEST description)
cc-transcript/<case>/children/<agentId>.input.jsonl      optional: a subagent file
cc-transcript/<case>/children/<agentId>.expected.json    its items ({"items": [...]})
```

## facts.json

```json
{
  "live": false,
  "turns": 2,
  "per_turn": [
    {"id": "<turn id = the opening row's uuid>", "outcome": "done|interrupted|failed|running",
     "user_source": "user|queued|peer|slash|bash|task|scheduled|", "error_kind": "rate_limit"}
  ],
  "steps": [
    {"id": "toolu_…", "kind": "edit|execute|read|search|fetch|task|other",
     "status": "running|done|failed|denied", "denial": "user-rejected"}
  ],
  "outputs": [
    {"step": "toolu_…", "total_lines": 1200, "total_bytes": 21599, "keep": "tail", "truncated": true}
  ],
  "shapes": ["queued_absorbed"]
}
```

- `live` is whether the session is still live: `false` closes the last open
  turn as `done` (`SetLive(false)`); `expected.json` is made the same way.
- `per_turn` lists every turn in order; `user_source` is the source of the
  turn's first user item (empty when it has none); `error_kind` only for a
  failed turn.
- `steps` lists **every** step in order of appearance; `denial` only when
  denied.
- `outputs` must list every output that is truncated (a non-truncated one may
  be listed too); `total_*` count the whole text, `keep` is the end kept.
- `shapes` declares the rule shapes the case demonstrates that the structured
  part cannot show. `TestFixtures_CoverRuleShapes` unions them with the shapes
  it derives from `steps`, `outputs` and `per_turn` (every denial value, a
  failed step, execute `keep: tail`, read `keep: head`, every `user_source`)
  and fails naming any missing one. Declared shapes: `queued_prompt_row`,
  `queued_absorbed`, `interrupted_marker`, `interrupted_refusal`,
  `interrupted_killed`, `failed_api_error`; a declared shape needs evidence in
  the structured part where there can be (a queued turn, an interrupted turn,
  a failed turn with `error_kind`). `resumed` is exempt until the transcript
  shows it (M-U1-4-a).
- Write `facts.json` by **reading `input.jsonl`**, not by looking at
  `expected.json` or the normalizer output. If a fact and the normalizer
  disagree, decide by reading the row: a wrong fact is fixed, a wrong
  normalizer is reported.

## Scrubbing

`input.jsonl` files come from `internal/convmodel/ccnorm/cmd/scrubfixture`
(rules in package `scrub`): it keeps the rows and fields the normalizer reads
(`ccnorm.ReadFields`) plus `cwd`, `sessionId`, `session_id`, `gitBranch` and
`version`, rewrites the identity (`cwd` → `/work/fixture`, session ids → the
fixed uuid, `gitBranch` → `main`), maps home and temp paths to `/work/…`, hides
the account name, e-mail addresses, tailnet addresses and secret-shaped
strings, and replaces every image's base64 data by a 1x1 PNG. **After
scrubbing, an image placeholder's `bytes` is the size of that tiny PNG (70 bytes),
not of the original.** Message text is kept as recorded, so only record
throwaway prompts. Output is deterministic and scrubbing it again changes
nothing. `TestFixtures_NoPrivateData` refuses a home path, a tailnet address, an
e-mail address, a secret-shaped string or a 32+ character token (outside image
data) in any file here.

## Adding a case

1. Record a session (a throwaway tmux session in a scratch directory, prompts
   written for the fixture) and take its transcript, `~/.claude/projects/<dir>/<sid>.jsonl`.
   A subagent's own file is `<sid>/subagents/agent-<agentId>.jsonl`.
2. Scrub it, from the repo root:

   ```
   go run ./internal/convmodel/ccnorm/cmd/scrubfixture -in <raw>.jsonl \
     -out testdata/conversation/v1/cc-transcript/<case>/input.jsonl \
     -home "$HOME" -user "$(id -un)"
   ```

   For a subagent file write `…/<case>/children/<agentId>.input.jsonl`.
3. Hand-write, in the same directory: `README.md` (line 1 is the MANIFEST
   description; how it was recorded, what it covers, `live`), and `facts.json`
   (format above) by reading the scrubbed input.
4. Run `go test ./internal/convmodel/ccnorm -run TestGolden -update`. It
   rewrites `expected.json` (and `children/*.expected.json`) of every case
   directory and rebuilds `MANIFEST.json` (cases sorted by name; `description`
   from a one-line `DESCRIPTION` file if present, else line 1 of `README.md`;
   `cc_version` from the first row with a `version`; sha256 of every file).
   `facts.json` is never touched.
5. Run `go test ./internal/convmodel/...`. `TestFacts` must pass **without**
   changing `facts.json` to match; read `expected.json` once for sense.

To check the scrubber against the raw transcripts (it is also covered by a
built-in sample): `PDX_RAW_TRANSCRIPTS=<dir of raw .jsonl> go test ./internal/convmodel/ccnorm -run TestScrubber_KeepsEveryReadField`.

## Tests

`TestGolden` (input → expected.json byte for byte, `Validate`),
`TestManifest_Sha256Match`, `TestFacts`, `TestFixtures_NoPrivateData`,
`TestFixtures_CoverRuleShapes`, `TestScrubber_KeepsEveryReadField` — all in
`internal/convmodel/ccnorm`.

## Cases

The three `ios-*` cases are scrubbed from the iOS samples (Claude Code
2.1.292). `api-error`, `denial-kinds`, `source-kinds` and `output-caps` are
**synthetic**, built from the M-U1-7 shapes for rule shapes a recording cannot
reliably provoke; their READMEs say so.
