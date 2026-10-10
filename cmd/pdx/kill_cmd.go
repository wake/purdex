package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// runKillCmd implements `pdx kill <ref>` (spec §7.3): the target goes as
// typed, the daemon matches it among the caller's members only. A replay is
// safe (a killed member answers 200 again), so the POST is Idempotent.
func runKillCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx kill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && (len(pos) != 1 || strings.TrimSpace(pos[0]) == "") {
		err = errors.New("需要剛好一個 <ref>（_xxxxxx 或 <host>/<name>）")
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx kill: %v\n%s\n", err, killUsage)
		return ExitUsage
	}
	client, inbox, ok := teamSetup("kill", *cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	var m team.Member
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: inbox, Target: pos[0]}, &m, daemonclient.Idempotent()); err != nil {
		return teamReportErr("kill", err, stderr)
	}
	out, err := json.Marshal(m)
	if err != nil {
		fmt.Fprintf(stderr, "pdx kill: %v\n", err)
		return ExitError
	}
	fmt.Fprintln(stdout, string(out))
	return ExitOK
}
