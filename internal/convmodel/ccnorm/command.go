package ccnorm

import (
	"regexp"
	"strconv"

	"github.com/wake/purdex/internal/convmodel"
)

// commandOf is the command of an execute step, from its tool input; nil when
// the input has no command text. The text and description are capped like any
// input string (the step's input_truncated says when they were).
func commandOf(in object) *convmodel.Command {
	text, _ := capText(in.str("command"), convmodel.MaxInputString)
	if text == "" {
		return nil
	}
	desc, _ := capText(in.str("description"), convmodel.MaxInputString)
	return &convmodel.Command{Text: text, Description: desc}
}

// exitCodeRe reads the "Exit code N" a failed Bash result starts with.
var exitCodeRe = regexp.MustCompile(`^Exit code (-?[0-9]{1,9})(?:\D|$)`)

// commandWithResult is a copy of c with what a result adds: the exit code
// from a result text that starts "Exit code N", and the background task id
// from toolUseResult.backgroundTaskId.
func commandWithResult(c *convmodel.Command, text string, tur object) *convmodel.Command {
	out := *c
	out.ExitCode = nil
	if m := exitCodeRe.FindStringSubmatch(text); m != nil {
		if code, err := strconv.Atoi(m[1]); err == nil {
			out.ExitCode = &code
		}
	}
	out.BackgroundTaskID, _ = capText(tur.str("backgroundTaskId"), maxDenial*2)
	return &out
}
