package main

import (
	"errors"
	"testing"
)

func TestResolveAgentPID_DirectParent(t *testing.T) {
	orig := readResolverProcessInfo
	t.Cleanup(func() { readResolverProcessInfo = orig })

	readResolverProcessInfo = func(pid int) (procInfo, error) {
		switch pid {
		case 200:
			return procInfo{PID: 200, PPID: 100, ExePath: "/opt/bin/codex"}, nil
		default:
			return procInfo{}, errors.New("unexpected pid")
		}
	}

	got, uncertain := resolveAgentPID(200)
	if got != 200 {
		t.Fatalf("pid = %d, want 200", got)
	}
	if uncertain {
		t.Fatal("uncertain = true, want false")
	}
}

func TestResolveAgentPID_SkipsShell(t *testing.T) {
	orig := readResolverProcessInfo
	t.Cleanup(func() { readResolverProcessInfo = orig })

	readResolverProcessInfo = func(pid int) (procInfo, error) {
		switch pid {
		case 300:
			return procInfo{PID: 300, PPID: 200, ExePath: "/bin/sh", Argv: []string{"sh", "-c", "pdx hook SessionStart"}}, nil
		case 200:
			return procInfo{PID: 200, PPID: 100, ExePath: "/opt/bin/claude"}, nil
		default:
			return procInfo{}, errors.New("unexpected pid")
		}
	}

	got, uncertain := resolveAgentPID(300)
	if got != 200 {
		t.Fatalf("pid = %d, want 200", got)
	}
	if uncertain {
		t.Fatal("uncertain = true, want false")
	}
}

func TestResolveAgentPID_SkipsNpx(t *testing.T) {
	orig := readResolverProcessInfo
	t.Cleanup(func() { readResolverProcessInfo = orig })

	readResolverProcessInfo = func(pid int) (procInfo, error) {
		switch pid {
		case 300:
			return procInfo{PID: 300, PPID: 200, ExePath: "/opt/homebrew/bin/npx", Argv: []string{"npx", "pdx", "hook", "Stop"}}, nil
		case 200:
			return procInfo{PID: 200, PPID: 100, ExePath: "/opt/bin/codex"}, nil
		default:
			return procInfo{}, errors.New("unexpected pid")
		}
	}

	got, uncertain := resolveAgentPID(300)
	if got != 200 {
		t.Fatalf("pid = %d, want 200", got)
	}
	if uncertain {
		t.Fatal("uncertain = true, want false")
	}
}

func TestResolveAgentPID_SkipsMultipleShims(t *testing.T) {
	orig := readResolverProcessInfo
	t.Cleanup(func() { readResolverProcessInfo = orig })

	readResolverProcessInfo = func(pid int) (procInfo, error) {
		switch pid {
		case 400:
			return procInfo{PID: 400, PPID: 300, ExePath: "/bin/bash", Argv: []string{"bash", "-c", "npx pdx hook Stop"}}, nil
		case 300:
			return procInfo{PID: 300, PPID: 200, ExePath: "/opt/homebrew/bin/npx", Argv: []string{"npx", "pdx", "hook", "Stop"}}, nil
		case 200:
			return procInfo{PID: 200, PPID: 100, ExePath: "/opt/bin/claude"}, nil
		default:
			return procInfo{}, errors.New("unexpected pid")
		}
	}

	got, uncertain := resolveAgentPID(400)
	if got != 200 {
		t.Fatalf("pid = %d, want 200", got)
	}
	if uncertain {
		t.Fatal("uncertain = true, want false")
	}
}

func TestResolveAgentPID_ReachesInit(t *testing.T) {
	orig := readResolverProcessInfo
	t.Cleanup(func() { readResolverProcessInfo = orig })

	readResolverProcessInfo = func(pid int) (procInfo, error) {
		switch pid {
		case 300:
			return procInfo{PID: 300, PPID: 1, ExePath: "/bin/sh", Argv: []string{"sh", "-c", "pdx hook Stop"}}, nil
		default:
			return procInfo{}, errors.New("unexpected pid")
		}
	}

	got, uncertain := resolveAgentPID(300)
	if got != 1 {
		t.Fatalf("pid = %d, want 1", got)
	}
	if !uncertain {
		t.Fatal("uncertain = false, want true")
	}
}
