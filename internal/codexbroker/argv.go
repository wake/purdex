package codexbroker

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mattn/go-shellwords"
)

// ErrArgvTruncated is returned by ParseBrokerArgv when the broker process
// argv string is missing the expected `serve --cwd ...` shape; surfaces as
// AnomalyArgvTruncated on the BrokerRecord.
var ErrArgvTruncated = errors.New("broker argv truncated or missing serve+cwd shape")

// BrokerArgv is the parsed view of the broker's command line.
type BrokerArgv struct {
	Cwd      string
	Endpoint string
	PidFile  string
}

// ParseBrokerArgv extracts --cwd / --endpoint / --pid-file from the broker's
// command-line string. Supports both the space-separated and `--flag=value`
// shapes, and quoted values via the standard shell-words tokeniser.
//
// Returns ErrArgvTruncated when:
//   - tokenisation fails outright, or
//   - the token list does not contain a literal "serve" before any of the
//     expected flags (the broker is invoked as `... app-server-broker.mjs serve ...`),
//     or
//   - --cwd is absent after `serve`.
//
// Endpoint and pid-file are optional in the returned struct (some test
// fixtures may omit them); callers tag those independently.
func ParseBrokerArgv(cmdline string) (BrokerArgv, error) {
	parser := shellwords.NewParser()
	tokens, err := parser.Parse(cmdline)
	if err != nil {
		return BrokerArgv{}, fmt.Errorf("%w: tokenise: %v", ErrArgvTruncated, err)
	}

	// Find the index of the literal "serve" subcommand.
	serveIdx := -1
	for i, tok := range tokens {
		if tok == "serve" {
			serveIdx = i
			break
		}
	}
	if serveIdx < 0 {
		return BrokerArgv{}, fmt.Errorf("%w: missing serve keyword", ErrArgvTruncated)
	}

	var argv BrokerArgv
	for i := serveIdx + 1; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "--cwd" && i+1 < len(tokens):
			argv.Cwd = tokens[i+1]
			i++
		case strings.HasPrefix(tok, "--cwd="):
			argv.Cwd = strings.TrimPrefix(tok, "--cwd=")
		case tok == "--endpoint" && i+1 < len(tokens):
			argv.Endpoint = tokens[i+1]
			i++
		case strings.HasPrefix(tok, "--endpoint="):
			argv.Endpoint = strings.TrimPrefix(tok, "--endpoint=")
		case tok == "--pid-file" && i+1 < len(tokens):
			argv.PidFile = tokens[i+1]
			i++
		case strings.HasPrefix(tok, "--pid-file="):
			argv.PidFile = strings.TrimPrefix(tok, "--pid-file=")
		}
	}

	if argv.Cwd == "" {
		return argv, fmt.Errorf("%w: missing --cwd", ErrArgvTruncated)
	}
	return argv, nil
}
