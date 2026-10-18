package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPdxTestCommandLine(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := testMain([]string{"-h"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "usage: pdx test") {
		t.Fatalf("help: code %d out %q", code, out.String())
	}
	for name, argv := range map[string][]string{
		"no task":   {},
		"bad class": {"lint", "--class", "urgent"},
		"list+task": {"--list", "lint"},
	} {
		out.Reset()
		errOut.Reset()
		if code := testMain(argv, &out, &errOut); code != 4 || !strings.Contains(errOut.String(), "pdx test:") {
			t.Errorf("%s: code %d err %q", name, code, errOut.String())
		}
	}
}
