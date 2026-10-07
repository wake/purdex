package modevents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPath_DefaultFits(t *testing.T) {
	p, ok := SocketPath("/Users/wake/.config/pdx")
	if !ok || p != "/Users/wake/.config/pdx/mod.sock" {
		t.Fatalf("SocketPath = %q, %v; want the mlab default, ok", p, ok)
	}

	// A relative data dir is made absolute: the mod dials the path from
	// another working directory.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p, _ = SocketPath("data")
	if want := filepath.Join(wd, "data", "mod.sock"); p != want {
		t.Fatalf("relative SocketPath = %q, want %q", p, want)
	}
}

func TestSocketPath_TooLongIsNotOK(t *testing.T) {
	dir := "/" + strings.Repeat("d", 119) // 120 bytes
	p, ok := SocketPath(dir)
	if ok {
		t.Fatalf("a %d-byte socket path must not be ok", len(p))
	}
	if p != dir+"/mod.sock" {
		t.Fatalf("the path is still reported (for logs and the read API): %q", p)
	}

	// The boundary: exactly 100 bytes fits, 101 does not.
	fit := "/" + strings.Repeat("e", 100-len("/mod.sock")-1)
	if p, ok := SocketPath(fit); !ok || len(p) != 100 {
		t.Fatalf("100-byte path: len=%d ok=%v", len(p), ok)
	}
	if p, ok := SocketPath(fit + "e"); ok || len(p) != 101 {
		t.Fatalf("101-byte path: len=%d ok=%v", len(p), ok)
	}
}
