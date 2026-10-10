Hand-composed session: four turns that each ask an AskUserQuestion, answered four different ways (U3-0).

- **Not recorded.** The rows are written to the shapes measured in real Claude Code transcripts (2.1.2xx): the `tool_use` input `{questions:[{question, header, multiSelect, options:[{label, description}]}]}`, and a result row whose `toolUseResult.answers` is an object keyed by the question text, one string each (a multi-select answer is one string joining the picks with ", " and quoting a pick that contains a comma). Text is invented for the fixture; `version` says 2.1.294 only so the MANIFEST has one. Run through the scrub rules (`/work/fixture`, the fixed session id) so it is a scrub fixed point. The dismissed result's wording is the one Collie's REFUSED pattern names ("dismissed the question"); a real dismissal row was not captured.
- Turn 1: one call, two questions: a single-select answered `乙案`, and a multi-select answered `紅, "藍, 深"` (a label that contains a comma), read back as the two options `紅` and `藍, 深`.
- Turn 2: a single question answered with free text (`晚上七點`, an "Other" answer that names no option), kept whole.
- Turn 3: the question dismissed: `is_error` result with the text "The user dismissed the question without answering." → the step is `denied` / `user-rejected` and has no answers.
- Turn 4: a question with no result yet (the session is live and the turn is `running`): the step is `running`, the question is there, `answers` is not.
- Live: `true`.
