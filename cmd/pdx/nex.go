// cmd/pdx/nex.go
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/wake/purdex/internal/config"
	"lab.protype.tw/wake/nexen/cmd/nex/client"
)

// nexProbeTimeout bounds probeNexCapabilities' single local round trip to
// this daemon's /v1/capabilities — used only on the error path, to tell
// "nex disabled on this host" apart from any other failure.
const nexProbeTimeout = 10 * time.Second

// nexUsage is printed (to stderr) for a malformed `pdx nex` invocation —
// an unrecognized or incomplete --addr/--token/--config flag. --addr,
// --token and --config must come BEFORE the subcommand: the stdlib flag
// package stops parsing at the first non-flag argument, so a flag placed
// after the subcommand is handed to client.Run instead, which reports its
// own (unrelated-looking) usage error. Once flag parsing succeeds, every
// remaining argument (including a bare "delegate" with no further flags,
// or none at all) is client.Run's grammar to enforce, not this command's.
const nexUsage = "usage: pdx nex [--addr <url>] [--token <t>] [--config <path>] <subcommand> [args...]\n" +
	"       (--addr/--token/--config must come before the subcommand)\n" +
	"       subcommands: delegate, ls, show, watch, events, attach, send, interrupt, archive, terminate, host"

// runNexMain is the `pdx nex` switch target: it wires runNex to the real
// process environment, config.Load, and probeNexCapabilities.
func runNexMain(args []string) {
	os.Exit(runNex(args, os.Stdout, os.Stderr, os.Getenv, config.Load, probeNexCapabilities))
}

// runNex resolves the nex API's base URL and bearer token — --addr/--token
// flags, then PDX_NEX_ADDR/PDX_NEX_TOKEN env vars, then config.toml's
// bind/port/token — and calls client.Run(stdout, <remaining args>, base,
// token), lab.protype.tw/wake/nexen's own CLI grammar (delegate, ls, show,
// watch, events, attach, send, interrupt, archive, terminate, host).
//
// --addr, when given (by flag or env), is used verbatim as the base URL —
// the caller supplies the full base including /api/nex if that is where it
// lives; the config-derived default is "http://<Bind>:<Port>/api/nex".
// config.toml is loaded (via lookupConfig, config.Load in production) only
// when at least one of addr/token is still unresolved after flags and env,
// so a fully-flagged invocation never touches disk.
//
// lookupConfig matches config.Load's own signature (path -> value, not
// pointer) so production code can pass config.Load directly; tests inject
// a stub that returns a canned config without writing one to disk.
//
// On a nil error from client.Run, returns 0. On a non-nil error with no
// subcommand at all (client.Run's own "no subcommand given" usage error,
// which never touched the network), the error is printed as-is and this
// returns 1 without consulting probe. Otherwise probe(base, token) is
// called exactly once: a clean 404 means nex is not mounted on this host
// at all, so that is reported instead of client.Run's (often confusing,
// "404 page not found") error; any other outcome — probe error, or a
// non-404 status — means the daemon does serve nex, so client.Run's
// original error is printed verbatim (e.g. a 404 execution_not_found for
// an unknown execution id must not be mistaken for nex being disabled).
// Every error path returns 1; a malformed --addr/--token/--config flag
// returns 2 without loading config or making any request.
func runNex(args []string, stdout, stderr io.Writer, env func(string) string,
	lookupConfig func(path string) (config.Config, error),
	probe func(base, token string) (int, error)) int {

	fs := flag.NewFlagSet("nex", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, nexUsage) }
	addrFlag := fs.String("addr", "", "base URL of the nex API, e.g. http://host:7860/api/nex (default: derived from config.toml's bind/port)")
	tokenFlag := fs.String("token", "", "bearer token for the nex API (default: config.toml's token)")
	cfgPath := fs.String("config", "", "path to config.toml (default: ~/.config/pdx/config.toml)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	addr := *addrFlag
	if addr == "" {
		addr = env("PDX_NEX_ADDR")
	}
	token := *tokenFlag
	if token == "" {
		token = env("PDX_NEX_TOKEN")
	}

	if addr == "" || token == "" {
		cfg, err := lookupConfig(*cfgPath)
		if err != nil {
			fmt.Fprintf(stderr, "pdx nex: %v\n", err)
			return 1
		}
		if addr == "" {
			addr = fmt.Sprintf("http://%s:%d/api/nex", cfg.Bind, cfg.Port)
		}
		if token == "" {
			token = cfg.Token
		}
	}

	subArgs := fs.Args()
	err := client.Run(stdout, subArgs, addr, token)
	if err == nil {
		return 0
	}
	if len(subArgs) == 0 {
		// client.Run's own "no subcommand given" usage error: never made a
		// request, so there is nothing for probe to disambiguate.
		fmt.Fprintf(stderr, "pdx nex: %v\n", err)
		return 1
	}

	if status, probeErr := probe(addr, token); probeErr == nil && status == http.StatusNotFound {
		fmt.Fprintln(stderr, "nex: not enabled on this host (set [nex] enabled = true)")
		return 1
	}
	fmt.Fprintf(stderr, "pdx nex: %v\n", err)
	return 1
}

// probeNexCapabilities GETs <base>/v1/capabilities with the given bearer
// token and returns its status code — the error-path check for whether
// nex is mounted on this host at all (see runNex).
func probeNexCapabilities(base, token string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, base+"/v1/capabilities", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	httpClient := &http.Client{Timeout: nexProbeTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
