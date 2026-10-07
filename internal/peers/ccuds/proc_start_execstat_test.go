package ccuds

import (
	"os"
	"testing"

	"github.com/wake/purdex/internal/execstat"
)

func TestDefaultProcStartCountsPS(t *testing.T) {
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	if _, err := DefaultProcStart(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if n, d := execstat.PS.Snapshot(); n != 1 || d <= 0 {
		t.Fatalf("PS = (%d, %v), want (1, >0)", n, d)
	}
}

func TestDefaultProcStartCountsErrorPath(t *testing.T) {
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	if _, err := DefaultProcStart(2147483646); err == nil {
		t.Fatal("want error")
	}
	if n, _ := execstat.PS.Snapshot(); n != 1 {
		t.Fatalf("PS n = %d, want 1", n)
	}
}
