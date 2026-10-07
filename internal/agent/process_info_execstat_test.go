package agent

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/wake/purdex/internal/execstat"
)

func TestDefaultRunPSCountsSuccess(t *testing.T) {
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	if _, err := defaultRunPS(context.Background(), "-p", strconv.Itoa(os.Getpid()), "-o", "lstart="); err != nil {
		t.Fatal(err)
	}
	n, d := execstat.PS.Snapshot()
	if n != 1 || d <= 0 {
		t.Fatalf("PS = (%d, %v), want (1, >0)", n, d)
	}
}

func TestDefaultRunPSCountsErrorPath(t *testing.T) {
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	if _, err := defaultRunPS(context.Background(), "-p", "2147483646", "-o", "lstart="); err == nil {
		t.Fatal("want error for a nonexistent pid")
	}
	n, d := execstat.PS.Snapshot()
	if n != 1 || d <= 0 {
		t.Fatalf("PS = (%d, %v), want (1, >0) on the error path", n, d)
	}
}
