package proxyhelpertest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/proxyhelper"
)

// tempDirs is TempDirs in (registry, socks) order.
func tempDirs(t *testing.T) (reg, socks string) {
	t.Helper()
	socks, reg = TempDirs(t)
	return reg, socks
}

func configLine(t *testing.T, reg, socks string) []byte {
	t.Helper()
	b, err := json.Marshal(proxyhelper.Config{Name: "n", RegistryDir: reg, SockDir: socks, Version: "2.1.270", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func waitWithin(t *testing.T, p proxyhelper.Proc, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("Wait did not return within %v", d)
		return nil
	}
}

var variants = []struct {
	name string
	v    Variant
}{
	{"Normal", Normal},
	{"Broken", Broken},
	{"BrokenRegistered", BrokenRegistered},
	{"Refusing", Refusing},
	{"Barrier", Barrier},
}

func TestEveryVariantExitsOnContextCancel(t *testing.T) {
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			reg, socks := tempDirs(t)
			f := New(Options{Variant: tc.v})
			ctx, cancel := context.WithCancel(context.Background())
			p, err := f.Starter()(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p.Stdin().Write(configLine(t, reg, socks))
			go func() { drainStdout(p) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			waitWithin(t, p, time.Second)
		})
	}
}

func TestEveryVariantExitsOnStdinClose(t *testing.T) {
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			reg, socks := tempDirs(t)
			f := New(Options{Variant: tc.v})
			p, err := f.Starter()(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			p.Stdin().Write(configLine(t, reg, socks))
			go func() { drainStdout(p) }()
			time.Sleep(50 * time.Millisecond)
			p.Stdin().Close()
			waitWithin(t, p, time.Second)
		})
	}
}

func TestEveryVariantExitsOnSignal(t *testing.T) {
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			reg, socks := tempDirs(t)
			f := New(Options{Variant: tc.v})
			p, err := f.Starter()(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			p.Stdin().Write(configLine(t, reg, socks))
			// Nobody reads stdout: a real SIGKILL still ends the process.
			time.Sleep(50 * time.Millisecond)
			p.Signal(syscall.SIGKILL)
			if err := waitWithin(t, p, time.Second); !errors.Is(err, ErrKilled) {
				t.Errorf("Wait after SIGKILL = %v, want ErrKilled", err)
			}
			if f.Signals() != 1 {
				t.Errorf("Signals = %d, want 1", f.Signals())
			}
		})
	}
}

func TestPIDsAreUniqueAndStartAt900000(t *testing.T) {
	f := New(Options{Variant: Broken})
	seen := map[int]bool{}
	for i := 0; i < 3; i++ {
		peek := PeekPID()
		ctx, cancel := context.WithCancel(context.Background())
		p, err := f.Starter()(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p.PID() < 900000 || seen[p.PID()] || p.PID() != peek || f.LastPID() != p.PID() {
			t.Errorf("pid %d (peek %d, last %d, seen %v)", p.PID(), peek, f.LastPID(), seen)
		}
		seen[p.PID()] = true
		cancel()
		waitWithin(t, p, time.Second)
	}
	if f.Spawns() != 3 {
		t.Errorf("Spawns = %d, want 3", f.Spawns())
	}
}

func TestBrokenRegisteredLeavesFilesAndDeadSocketBehind(t *testing.T) {
	reg, socks := tempDirs(t)
	f := New(Options{Variant: BrokenRegistered})
	pid := PeekPID()
	ctx, cancel := context.WithCancel(context.Background())
	p, err := f.Starter()(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.Stdin().Write(configLine(t, reg, socks))

	jsonPath := filepath.Join(reg, strconv.Itoa(pid)+".json")
	sock := filepath.Join(socks, strconv.Itoa(pid)+".sock")
	deadline := time.Now().Add(2 * time.Second)
	for {
		keys, _ := filepath.Glob(filepath.Join(reg, strconv.Itoa(pid)+".*.key"))
		if Exists(jsonPath) && len(keys) == 1 && Exists(sock) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registration not observed: json %v keys %v sock %v", Exists(jsonPath), keys, Exists(sock))
		}
		time.Sleep(10 * time.Millisecond)
	}
	// While alive it listens.
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial live socket: %v", err)
	}
	c.Close()

	cancel()
	waitWithin(t, p, time.Second)
	// Killed before ready: everything stays on disk, the socket is dead.
	if !Exists(jsonPath) || !Exists(sock) {
		t.Errorf("a killed helper must leave its files: json %v sock %v", Exists(jsonPath), Exists(sock))
	}
	if _, err := net.DialTimeout("unix", sock, 500*time.Millisecond); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("dial dead socket = %v, want ECONNREFUSED", err)
	}
	want, _ := ProcStart(pid)
	if got := registryProcStart(t, jsonPath); got != want {
		t.Errorf("json procStart = %q, want %q", got, want)
	}
}

func TestProcStartIsValidLstart(t *testing.T) {
	a, err := ProcStart(900001)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ProcStart(900002)
	if a == b {
		t.Errorf("ProcStart should differ per pid: %q", a)
	}
	if _, err := peers.ParseProcStart(a); err != nil {
		t.Errorf("ProcStart %q does not parse: %v", a, err)
	}
}

func registryProcStart(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ProcStart string `json:"procStart"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.ProcStart
}

// drainStdout drains a proc's stdout so a Normal variant never wedges on a
// full pipe.
func drainStdout(p proxyhelper.Proc) {
	buf := make([]byte, 4096)
	for {
		if _, err := p.Stdout().Read(buf); err != nil {
			return
		}
	}
}
