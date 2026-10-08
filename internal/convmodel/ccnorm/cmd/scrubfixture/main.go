// Command scrubfixture turns a raw Claude Code transcript into a golden
// fixture input (testdata/conversation/v1/cc-transcript/<case>/input.jsonl).
// It is a `go run` tool, not part of the pdx binary:
//
//	go run ./internal/convmodel/ccnorm/cmd/scrubfixture -in raw.jsonl -out input.jsonl
//
// With no -in / -out it reads stdin and writes stdout. -home and -user name
// the account to hide (default: the current one); repeat -user for more. The
// rules are in package scrub.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
	var names users
	fs.Var(&names, "user", "account name to hide, repeatable (default the current account)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	in := stdin
	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			fmt.Fprintln(stderr, "scrubfixture:", err)
			return 1
		}
		defer f.Close()
		in = f
	}
	out := stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintln(stderr, "scrubfixture:", err)
			return 1
		}
		defer f.Close()
		out = f
	}
	rep, err := scrub.Scrub(in, out, scrub.Options{Home: *home, Users: names})
	if err != nil {
		fmt.Fprintln(stderr, "scrubfixture:", err)
		return 1
	}
	reasons := make([]string, 0, len(rep.Dropped))
	for k, n := range rep.Dropped {
		reasons = append(reasons, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(reasons)
	fmt.Fprintf(stderr, "scrubfixture: kept %d of %d rows; dropped %s\n", rep.Kept, rep.Rows, strings.Join(reasons, " "))
	return 0
}
