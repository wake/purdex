package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// namesUsage is the grammar of `pdx names`.
const namesUsage = "usage: pdx names [--json] [--config <path>]"

// namesAttempt bounds each request of `pdx names`: the /team mod asks before it does anything else, so a stuck daemon must cost it
// a few seconds, not the client's whole restart-wait.
const namesAttempt = 3 * time.Second

// leadNameCode is the shape of a persona code as `--lead-name` and `pdx names` take it: lower-case letters (the daemon holds the
// real list and checks the code against it; this only keeps a malformed word off the wire).
var leadNameCode = regexp.MustCompile(`^[a-z]{2,16}$`)

// namesPersona is one persona the person can name a lead by.
type namesPersona struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// namesAnswer is `pdx names --json`: the active personas and whether the daemon takes a lead name (team.lead_name.v1). Without that
// capability Personas is empty and the personas route is not read.
type namesAnswer struct {
	Personas []namesPersona `json:"personas"` // never null
	LeadName bool           `json:"lead_name"`
}

// runNames implements `pdx names`.
func runNames(args []string) {
	os.Exit(runNamesCmd(context.Background(), args, os.Stdout, os.Stderr))
}

// runNamesCmd is `pdx names [--json]` (name pool v2 spec §5.3): what the /team mod needs to tell a persona name from the words of
// a note. Reads GET /api/info for team.lead_name.v1 and, only when it is there, GET /api/hostconfig/personas for the active codes.
// Anything unreadable is a non-zero exit with nothing on stdout: the mod then runs /team as it did before names existed.
func runNamesCmd(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx names", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", "")
	asJSON := fs.Bool("json", false, "")
	err := fs.Parse(args)
	if err == nil && fs.NArg() != 0 {
		err = fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx names: %v\n%s\n", err, namesUsage)
		return ExitUsage
	}
	client, code := relayClient(*cfgPath, stderr, namesAttempt, clientOpts)
	if code != ExitOK {
		return code
	}
	answer := namesAnswer{Personas: []namesPersona{}}
	var info struct {
		Capabilities []string `json:"capabilities"`
	}
	if _, err := client.Do(ctx, http.MethodGet, "/api/info", nil, &info); err != nil {
		return namesFail(stderr, err)
	}
	if answer.LeadName = slices.Contains(info.Capabilities, team.CapabilityLeadName); answer.LeadName {
		var raw struct {
			Items []struct {
				Code    string `json:"code"`
				Name    string `json:"name"`
				Retired bool   `json:"retired"`
			} `json:"items"`
		}
		if _, err := client.Do(ctx, http.MethodGet, "/api/hostconfig/personas", nil, &raw); err != nil {
			return namesFail(stderr, err)
		}
		for _, p := range raw.Items {
			if !p.Retired && leadNameCode.MatchString(p.Code) {
				answer.Personas = append(answer.Personas, namesPersona{Code: p.Code, Name: sanitizeCell(p.Name)})
			}
		}
	}
	if *asJSON {
		out, err := json.Marshal(answer)
		if err != nil {
			fmt.Fprintf(stderr, "pdx names: %v\n", err)
			return ExitError
		}
		fmt.Fprintln(stdout, string(out))
		return ExitOK
	}
	if !answer.LeadName {
		fmt.Fprintln(stdout, "這個 daemon 還不支援為 lead 指定名字（沒有 team.lead_name.v1）")
		return ExitOK
	}
	for _, p := range answer.Personas {
		fmt.Fprintf(stdout, "%-14s %s\n", p.Code, strings.TrimSpace(p.Name))
	}
	return ExitOK
}

// namesFail maps a failed request to one stderr line and an exit code.
func namesFail(stderr io.Writer, err error) int {
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx names: daemon 沒有回應 daemon_unavailable")
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx names: 這個 daemon 沒有需要的路由 unsupported")
		return ExitUnsupported
	default:
		fmt.Fprintf(stderr, "pdx names: %v\n", err)
		return ExitError
	}
}
