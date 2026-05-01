package codexbroker

import (
	"errors"
	"testing"
)

// TestParseArgv_StandardForm parses the canonical mlab observation: argv
// containing `serve --cwd /path --endpoint unix:/sock --pid-file /pid`.
func TestParseArgv_StandardForm(t *testing.T) {
	cmd := "node /Users/wake/.claude/plugins/cache/openai-codex/codex/1.0.2/scripts/app-server-broker.mjs serve --cwd /Users/wake/Workspace/wake/purdex --endpoint unix:/var/folders/aa/bb/T/cxc-ABCDEF/broker.sock --pid-file /var/folders/aa/bb/T/cxc-ABCDEF/broker.pid"
	got, err := ParseBrokerArgv(cmd)
	if err != nil {
		t.Fatalf("ParseBrokerArgv: %v", err)
	}
	if got.Cwd != "/Users/wake/Workspace/wake/purdex" {
		t.Errorf("Cwd = %q", got.Cwd)
	}
	if got.Endpoint != "unix:/var/folders/aa/bb/T/cxc-ABCDEF/broker.sock" {
		t.Errorf("Endpoint = %q", got.Endpoint)
	}
	if got.PidFile != "/var/folders/aa/bb/T/cxc-ABCDEF/broker.pid" {
		t.Errorf("PidFile = %q", got.PidFile)
	}
}

// TestParseArgv_QuotedCwd handles a cwd containing spaces (quoted in argv).
func TestParseArgv_QuotedCwd(t *testing.T) {
	cmd := `node app-server-broker.mjs serve --cwd "/tmp/path with spaces" --endpoint unix:/tmp/x.sock --pid-file /tmp/x.pid`
	got, err := ParseBrokerArgv(cmd)
	if err != nil {
		t.Fatalf("ParseBrokerArgv: %v", err)
	}
	if got.Cwd != "/tmp/path with spaces" {
		t.Errorf("Cwd = %q, want %q", got.Cwd, "/tmp/path with spaces")
	}
}

// TestParseArgv_EqualsForm handles the GNU-style `--cwd=/path` form.
func TestParseArgv_EqualsForm(t *testing.T) {
	cmd := "node app-server-broker.mjs serve --cwd=/tmp/foo --endpoint=unix:/tmp/x.sock --pid-file=/tmp/x.pid"
	got, err := ParseBrokerArgv(cmd)
	if err != nil {
		t.Fatalf("ParseBrokerArgv: %v", err)
	}
	if got.Cwd != "/tmp/foo" {
		t.Errorf("Cwd = %q", got.Cwd)
	}
	if got.Endpoint != "unix:/tmp/x.sock" {
		t.Errorf("Endpoint = %q", got.Endpoint)
	}
	if got.PidFile != "/tmp/x.pid" {
		t.Errorf("PidFile = %q", got.PidFile)
	}
}

// TestParseArgv_TruncatedReturnsAnomaly returns ErrArgvTruncated when there's
// no `--cwd` after the `serve` keyword.
func TestParseArgv_TruncatedReturnsAnomaly(t *testing.T) {
	cmd := "node app-server-broker.mjs serve"
	_, err := ParseBrokerArgv(cmd)
	if !errors.Is(err, ErrArgvTruncated) {
		t.Errorf("expected ErrArgvTruncated, got %v", err)
	}
}

// TestParseArgv_UnquotedCwdWithSpaces documents what happens when ps strips
// argv quoting. macOS / Linux `ps -ww command=` typically preserves quoting,
// but on some BSD configurations or wrapper layers (e.g. systemd-run) the
// quote chars can be lost, leaving the cwd as a bare token. The parser must
// then take the first whitespace-delimited token as cwd; the remainder of
// the path is silently dropped. This is documented behaviour, not a bug:
// the resulting brokerKey will not match codex's hash, the broker shows up
// as process_orphan + state_dir_no_match, and an `argv_truncated` anomaly
// MAY (but is not required to) be attached. We just need to make sure we
// don't panic and don't fall through to the next flag boundary.
func TestParseArgv_UnquotedCwdWithSpaces(t *testing.T) {
	cmd := "node app-server-broker.mjs serve --cwd /tmp/path with spaces --endpoint unix:/tmp/x.sock"
	got, err := ParseBrokerArgv(cmd)
	if err != nil {
		t.Fatalf("ParseBrokerArgv: %v", err)
	}
	// First token after --cwd is taken; "with" and "spaces" are dropped.
	if got.Cwd != "/tmp/path" {
		t.Errorf("Cwd = %q, want %q (parser takes first token only)", got.Cwd, "/tmp/path")
	}
	// Endpoint must still be parsed correctly past the dropped tokens.
	if got.Endpoint != "unix:/tmp/x.sock" {
		t.Errorf("Endpoint = %q, want unix:/tmp/x.sock", got.Endpoint)
	}
}

// TestParseArgv_MissingServeKeyword returns truncated error.
func TestParseArgv_MissingServeKeyword(t *testing.T) {
	cmd := "node app-server-broker.mjs --cwd /tmp/foo"
	_, err := ParseBrokerArgv(cmd)
	if !errors.Is(err, ErrArgvTruncated) {
		t.Errorf("expected ErrArgvTruncated when serve missing, got %v", err)
	}
}
