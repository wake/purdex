package fastlane

import (
	"context"
	"io"
	"time"
)

// The fast lane runs a command with no gate in front of it. These are its own types, so it does not import internal/gate: the
// values below are the ones the gate prints and reads (the two sides talk to each other through them), and the tests hold them
// to literals, and to the gate's own values until the gate is gone.

// Class is the slot class a run asks for.
type Class string

const (
	ClassLight Class = "light"
	ClassHeavy Class = "heavy"
)

// ExitRefused is the exit code of a command that was not run (sysexits EX_TEMPFAIL).
const ExitRefused = 75

// ExitOrphaned is the exit code of a command whose caller went away while it waited: it was not run (129, as for a hangup).
const ExitOrphaned = 129

// AdmittedMarkEnv, when set, makes the runner print the admitted mark on the command's stderr right before it starts the command;
// its value is a nonce, which is in the mark lines. The runner takes the variable out of the command's environment.
const AdmittedMarkEnv = "PDX_GATE_ADMITTED_MARK"

// AdmittedMark is the line printed for AdmittedMarkEnv=nonce: a record separator (0x1e), so it is not a line a command prints by
// accident, and the nonce.
func AdmittedMark(nonce string) string { return "\x1epdx-gate-admitted:" + nonce + "\n" }

// NotStartedMark follows AdmittedMark when the command could not be started after all: the run was admitted but nothing ran.
func NotStartedMark(nonce string) string { return "\x1epdx-gate-not-started:" + nonce + "\n" }

// Options is one run.
type Options struct {
	Argv  []string // the command as typed; argv[0] is its name
	Real  string   // the binary to run in place of argv[0]
	Class Class    // the slot class the run asks for (the direct runner takes no slot)
	Env   []string
	Dir   string
	// WaitMax is how long the command may wait for a slot; WaitedOut, when set, is written when Run returns: true when the wait ran
	// past WaitMax and that is why nothing ran. A runner that takes no slot never sets it.
	WaitMax   time.Duration
	WaitedOut *bool
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

// Gater runs a command; Direct is the one in use.
type Gater interface {
	Run(ctx context.Context, o Options) (int, error)
}
