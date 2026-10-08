// Command scrubfixture turns a raw Claude Code transcript into a golden
// fixture input (testdata/conversation/v1/cc-transcript/<case>/input.jsonl).
// It is a `go run` tool, not part of the pdx binary:
//
//	go run ./internal/convmodel/ccnorm/cmd/scrubfixture -in raw.jsonl -out input.jsonl
//
// With no -in / -out it reads stdin and writes stdout. -home and -user name
// the account to hide (default: the current one); repeat -user for more. The
// rules are in package scrub.
//
// It fails (exit 1, nothing written) on a row that is not a JSON object or not
// valid UTF-8, naming the line but never its content; -allow-bad-rows counts
// and skips such rows instead. Size limits (16 MiB a line, 256 MiB in all,
// nesting depth 64) always fail. -out is written to a temporary file in its
// directory and renamed over the target only after a complete scrub, so a
// failure leaves no partial output, and -in and -out may not be the same file.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/convmodel/ccnorm/scrub"
)

type users []string

func (u *users) String() string     { return strings.Join(*u, ",") }
func (u *users) Set(v string) error { *u = append(*u, v); return nil }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrubfixture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inPath := fs.String("in", "", "raw transcript (default stdin)")
	outPath := fs.String("out", "", "fixture input to write (default stdout)")
	home := fs.String("home", "", "home directory to hide (default the current one)")
	allowBad := fs.Bool("allow-bad-rows", false, "count and skip rows that are not JSON objects or not UTF-8 instead of failing")
	var names users
	fs.Var(&names, "user", "account name to hide, repeatable (default the current account)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "scrubfixture:", err)
		return 1
	}
	in := stdin
	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		in = f
		if *outPath != "" {
			if same, err := sameFile(*inPath, *outPath); err != nil {
				return fail(err)
			} else if same {
				return fail(errors.New("-in and -out are the same file; refusing to overwrite the input"))
			}
		}
	}

	// the whole scrub happens before anything reaches the destination
	var buf bytes.Buffer
	var tmp *os.File
	var w io.Writer = &buf
	if *outPath != "" {
		f, err := os.CreateTemp(filepath.Dir(*outPath), ".scrubfixture-*.tmp")
		if err != nil {
			return fail(err)
		}
		tmp = f
		defer func() { // still here only when the run did not finish
			if tmp != nil {
				tmp.Close()
				os.Remove(tmp.Name())
			}
		}()
		w = bufio.NewWriter(f)
	}
	rep, err := scrub.Scrub(in, w, scrub.Options{Home: *home, Users: names, AllowBadRows: *allowBad})
	if err != nil {
		return fail(err)
	}
	if tmp != nil {
		if err := w.(*bufio.Writer).Flush(); err != nil {
			return fail(err)
		}
		if err := tmp.Sync(); err != nil {
			return fail(err)
		}
		if err := tmp.Chmod(0o644); err != nil {
			return fail(err)
		}
		if err := tmp.Close(); err != nil {
			return fail(err)
		}
		if err := os.Rename(tmp.Name(), *outPath); err != nil {
			return fail(err)
		}
		tmp = nil
	} else if _, err := stdout.Write(buf.Bytes()); err != nil {
		return fail(err)
	}
	reasons := make([]string, 0, len(rep.Dropped))
	for k, n := range rep.Dropped {
		reasons = append(reasons, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(reasons)
	fmt.Fprintf(stderr, "scrubfixture: kept %d of %d rows; dropped %s\n", rep.Kept, rep.Rows, strings.Join(reasons, " "))
	return 0
}

// sameFile reports whether a and b are one file: equal once cleaned, or the
// same inode (a symlink or hard link to it, when b already exists).
func sameFile(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	if absA == absB {
		return true, nil
	}
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}
