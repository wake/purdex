package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestReexecPlanOverrideExecPath(t *testing.T) {
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }

	p := &reexecPlan{path: "/home/u/.config/pdx/bin/pdx"}
	p.overrideExecPath("", logf)
	if p.path != "/home/u/.config/pdx/bin/pdx" || len(logs) != 0 {
		t.Fatalf("empty override must keep the booted path silently, got %q %v", p.path, logs)
	}

	p.overrideExecPath("/repo/bin/pdx", logf)
	if p.path != "/repo/bin/pdx" {
		t.Fatalf("the rebuilt binary must be what re-exec runs, got %q", p.path)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "/repo/bin/pdx") {
		t.Fatalf("override must be logged, got %v", logs)
	}
}
