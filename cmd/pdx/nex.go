// cmd/pdx/nex.go
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	"       env: PDX_NEX_ADDR, PDX_NEX_TOKEN — prefer PDX_NEX_TOKEN over --token\n" +
	"       (argv is visible to other local users; the environment is not)\n" +
	"       --addr requires --token or PDX_NEX_TOKEN: config.toml's token is only used for the local daemon\n" +
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
// lives — and then REQUIRES a token from --token / PDX_NEX_TOKEN: the
// local config.toml token is the credential of this host's daemon and is
// never sent to an address the user typed (exit 2, no config load, no
// request). The config-derived default is nexBaseURL (bind/port with a
// wildcard bind resolved to loopback). config.toml is loaded (via
// lookupConfig, config.Load in production) only when neither addr nor
// token was supplied, so a fully-specified invocation never touches disk.
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
// at all, so that is reported (naming the probed URL, since a wrong
// --addr looks identical) instead of client.Run's (often confusing,
// "404 page not found") error; any other outcome — probe error, or a
// non-404 status — means the daemon does serve nex, so client.Run's
// original error is printed verbatim (e.g. a 404 execution_not_found for
// an unknown execution id must not be mistaken for nex being disabled).
// Every error path returns 1; a malformed --addr/--token/--config flag,
// or --addr without a token, returns 2 without loading config or making
// any request.
func runNex(args []string, stdout, stderr io.Writer, env func(string) string,
	lookupConfig func(path string) (config.Config, error),
	probe func(base, token string) (int, error)) int {

	fs := flag.NewFlagSet("nex", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, nexUsage) }
	addrFlag := fs.String("addr", "", "base URL of the nex API, e.g. http://host:7860/api/nex (default: derived from config.toml's bind/port; requires --token or PDX_NEX_TOKEN)")
	tokenFlag := fs.String("token", "", "bearer token for the nex API (prefer PDX_NEX_TOKEN: argv is visible to other local users; default: config.toml's token, local daemon only)")
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

	if addr != "" && token == "" {
		fmt.Fprintln(stderr, "pdx nex: --addr given without --token / PDX_NEX_TOKEN (the local config token is not sent to a non-local address)")
		return 2
	}
	if addr == "" {
		cfg, err := lookupConfig(*cfgPath)
		if err != nil {
			fmt.Fprintf(stderr, "pdx nex: %v\n", err)
			return 1
		}
		addr = nexBaseURL(cfg)
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
		fmt.Fprintf(stderr, "nex: not enabled on this host (GET %s → 404; set [nex] enabled = true, or check --addr)\n", nexProbeURL(addr))
		return 1
	}
	fmt.Fprintf(stderr, "pdx nex: %v\n", err)
	return 1
}

// nexBaseURL derives the local daemon's nex base URL from config.toml's
// bind/port. A wildcard bind ("", 0.0.0.0, ::, [::]) is a listen address,
// not a dialable one, so it is rewritten to the matching loopback —
// 127.0.0.1 for the empty/IPv4 wildcard (as resolveDaemonHost does for
// the statusline proxy) and ::1 for the IPv6 one. IPv6 literals are
// bracketed by net.JoinHostPort.
func nexBaseURL(cfg config.Config) string {
	host := strings.Trim(cfg.Bind, "[]")
	switch host {
	case "", "0.0.0.0":
		host = resolveDaemonHost(host)
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(cfg.Port)) + "/api/nex"
}

// nexProbeURL is the URL probeNexCapabilities requests for base.
func nexProbeURL(base string) string { return base + "/v1/capabilities" }

// probeNexCapabilities GETs <base>/v1/capabilities with the given bearer
// token and returns its status code — the error-path check for whether
// nex is mounted on this host at all (see runNex).
func probeNexCapabilities(base, token string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, nexProbeURL(base), nil)
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
