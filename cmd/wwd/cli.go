package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// subcommands are the words `wwd` takes first, other than the daemon's flags. The usage lists each (a test holds the list and the handlers
// together).
var subcommands = []string{"install", "uninstall", "orphans-scan", "alerts-replay"}

const usageText = `usage: wwd [--config FILE]              run the daemon (the default)
       wwd --version                    print the version
       wwd help | -h | --help           print this
       wwd install --alias NAME [--token-file FILE] [--no-restart] [--dry-run]
                                        install or update the service (launchd or systemd)
       wwd uninstall [--purge]          remove the service (--purge: also the config, the token and the data)
       wwd orphans-scan [--measure N]   scan this host's process table for leaked dev processes, once, and print
       wwd alerts-replay --db COPY --work-root DIR [--days N] [--known LIST]
                                        count what the alert rules would have raised over a copy of wwd.db
`

type cliAction int

const (
	cliRun     cliAction = iota // start the daemon
	cliVersion                  // print the version
	cliExit                     // the usage or a mistake was said: exit with the code
)

// parseCLI reads what is left of the arguments once the subcommands (install, uninstall, orphans-scan, alerts-replay) have not taken them. It
// reads nothing but the arguments: wwd.db is not opened and the config is not read for a request for the usage or a mistake (#3843: `wwd help`
// used to start the daemon). The usage goes to out for a request (exit 0) and to errb with the mistake for a mistake (exit 2).
func parseCLI(args []string, out, errb io.Writer) (cfgPath string, action cliAction, code int) {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "-help", "--help":
			fmt.Fprint(out, usageText)
			return "", cliExit, 0
		}
	}
	fs := flag.NewFlagSet("wwd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", "", "config file (default ~/.config/wwd/config.toml)")
	version := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(out, usageText)
			return "", cliExit, 0
		}
		fmt.Fprintf(errb, "wwd: %v\n%s", err, usageText)
		return "", cliExit, 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errb, "wwd: unknown command %q (%s)\n%s", fs.Arg(0), strings.Join(subcommands, ", "), usageText)
		return "", cliExit, 2
	}
	if *version {
		return *config, cliVersion, 0
	}
	return *config, cliRun, 0
}
