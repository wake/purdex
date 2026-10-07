package modevents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSocketPath_DefaultFits(t *testing.T) {
	p, ok := ResolveSocketPath("/Users/wake/.config/pdx")
	if !ok || p != "/Users/wake/.config/pdx/mod.sock" {
		t.Fatalf("ResolveSocketPath = %q, %v; want the mlab default, ok", p, ok)
	}

	// A relative data dir is made absolute: the mod dials the path from
	// another working directory. "data" does not exist, so it is not
	// resolved either.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p, _ = ResolveSocketPath("data")
	if want := filepath.Join(wd, "data", "mod.sock"); p != want {
		t.Fatalf("relative ResolveSocketPath = %q, want %q", p, want)
	}
}

func TestResolveSocketPath_TooLongIsNotOK(t *testing.T) {
	dir := "/" + strings.Repeat("d", 119) // 120 bytes
	p, ok := ResolveSocketPath(dir)
	if ok {
		t.Fatalf("a %d-byte socket path must not be ok", len(p))
	}
	if p != dir+"/mod.sock" {
		t.Fatalf("the path is still reported (for logs and the read API): %q", p)
	}

	// The boundary: exactly 100 bytes fits, 101 does not.
	fit := "/" + strings.Repeat("e", 100-len("/mod.sock")-1)
	if p, ok := ResolveSocketPath(fit); !ok || len(p) != 100 {
		t.Fatalf("100-byte path: len=%d ok=%v", len(p), ok)
	}
	if p, ok := ResolveSocketPath(fit + "e"); ok || len(p) != 101 {
		t.Fatalf("101-byte path: len=%d ok=%v", len(p), ok)
	}
}

// The daemon binds where the data dir points (Listen), so a short symlink
// to a long directory gives a socket path the channel refuses.
func TestResolveSocketPath_ShortLinkToLongTargetIsNotOK(t *testing.T) {
	long := filepath.Join(shortDir(t), strings.Repeat("d", 90))
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(shortDir(t), "l")
	if err := os.Symlink(long, short); err != nil {
		t.Fatal(err)
	}
	if p := filepath.Join(short, SocketName); len(p) > MaxSocketPath {
		t.Fatalf("setup: %s is already too long", p)
	}
	resolved, err := filepath.EvalSymlinks(long)
	if err != nil {
		t.Fatal(err)
	}

	p, ok := ResolveSocketPath(short)
	if ok {
		t.Fatalf("ResolveSocketPath(%s) = %q, ok; the resolved path is %d bytes", short, p, len(p))
	}
	if want := filepath.Join(resolved, SocketName); p != want {
		t.Fatalf("ResolveSocketPath = %q, want the resolved %q", p, want)
	}
}

// A long symlink to a short directory is fine: the socket is bound at the
// short, resolved path.
func TestResolveSocketPath_LongLinkToShortTargetIsOK(t *testing.T) {
	target := shortDir(t)
	link := filepath.Join(shortDir(t), strings.Repeat("l", 95))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if p := filepath.Join(link, SocketName); len(p) <= MaxSocketPath {
		t.Fatalf("setup: %s is not too long", p)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}

	p, ok := ResolveSocketPath(link)
	if want := filepath.Join(resolved, SocketName); !ok || p != want {
		t.Fatalf("ResolveSocketPath(%s) = %q, %v; want %q, ok", link, p, ok, want)
	}
	// Listen agrees: it binds at that path.
	l := mustListen(t, p)
	defer l.Close()
	if got := l.Addr().String(); got != p {
		t.Fatalf("Listen bound at %s, want %s", got, p)
	}
}
