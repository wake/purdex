package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/resources"
)

// #2470: a lease granted while the volume is under its hard floor carries a warning; the CLI shows it, so it is not only in
// the raw HTTP answer.

const diskWarning = "disk: low on disk, 2048 MiB free on the volume of the Go build cache /x (hard floor 3 GiB)"

func warningDaemon() *fakeLeaseDaemon {
	return &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return http.StatusCreated, resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, Mode: "lease", Warning: diskWarning}
	}}
}

// `pdx lease acquire` (the mod's call) keeps printing one JSON line; the warning is a field of it.
func TestAcquire_PrintsTheDiskWarning(t *testing.T) {
	fixedHolder(t)
	code, stdout, _ := driveLease(t, context.Background(), warningDaemon(), nil, "acquire", "--kind", "test-full", "--client-id", leaseCID)
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if l := parseAcquireLine(t, stdout); !l.Granted || l.Warning != diskWarning {
		t.Fatalf("line = %+v", l)
	}
	// no warning, no field
	_, stdout, _ = driveLease(t, context.Background(), &fakeLeaseDaemon{}, nil, "acquire", "--kind", "test-full", "--client-id", leaseCID)
	if strings.Contains(stdout, "warning") {
		t.Fatalf("a warning field without a warning: %q", stdout)
	}
}

// `pdx lease run` says it on stderr and still runs the command.
func TestRun_PrintsTheDiskWarningAndStillRuns(t *testing.T) {
	d := warningDaemon()
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "sh", "-c", "exit 0")
	if code != 0 || !strings.Contains(stderr, diskWarning) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := driveRunCmd(t, &fakeLeaseDaemon{}, nil, []string{"--kind", "build"}, "sh", "-c", "exit 0"); code != 0 || strings.Contains(stderr, "disk") {
		t.Fatalf("a warning without one: code=%d stderr=%q", code, stderr)
	}
}
