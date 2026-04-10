// internal/tmux/executor.go
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var ErrNoSession = errors.New("no such session")

type TmuxSession struct {
	ID   string // tmux session ID, e.g. "$0"
	Name string
	Cwd  string
}

// Executor abstracts tmux CLI for testability.
type Executor interface {
	ListSessions() ([]TmuxSession, error)
	NewSession(name, cwd string) error
	KillSession(name string) error
	RenameSession(oldName, newName string) error
	HasSession(name string) bool
	SendKeys(target, keys string) error
	SendKeysRaw(target string, keys ...string) error
	PaneCurrentCommand(target string) (string, error)
	PanePID(target string) (string, error)
	PaneChildCommands(target string) ([]string, error)
	CapturePaneContent(target string, lastN int) (string, error)
	PaneSize(target string) (cols, rows int, err error)
	ResizeWindow(target string, cols, rows int) error
	ResizeWindowAuto(target string) error
	SetWindowOption(target, option, value string) error
	SetHookGlobal(event, command string) error
	RemoveHookGlobal(event string) error
	ShowHooksGlobal() (string, error)
	TmuxAlive() bool
}

// --- Real Executor ---

type RealExecutor struct{}

func NewRealExecutor() *RealExecutor { return &RealExecutor{} }

func (r *RealExecutor) ListSessions() ([]TmuxSession, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{session_path}").Output()
	if err != nil {
		if strings.Contains(err.Error(), "no server running") ||
			strings.Contains(string(out), "no server running") {
			return nil, nil
		}
		// exit status 1 with "no sessions" is normal
		if exitErr, ok := err.(*exec.ExitError); ok {
			if strings.Contains(string(exitErr.Stderr), "no server running") ||
				strings.Contains(string(exitErr.Stderr), "no sessions") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	var sessions []TmuxSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		s := TmuxSession{ID: parts[0]}
		if len(parts) > 1 {
			s.Name = parts[1]
		}
		if len(parts) > 2 {
			s.Cwd = parts[2]
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (r *RealExecutor) NewSession(name, cwd string) error {
	return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", cwd).Run()
}

func (r *RealExecutor) KillSession(name string) error {
	err := exec.Command("tmux", "kill-session", "-t", name).Run()
	if err != nil {
		return ErrNoSession
	}
	return nil
}

func (r *RealExecutor) RenameSession(oldName, newName string) error {
	err := exec.Command("tmux", "rename-session", "-t", oldName, newName).Run()
	if err != nil {
		return fmt.Errorf("tmux rename-session: %w", err)
	}
	return nil
}

func (r *RealExecutor) HasSession(name string) bool {
	// Use "=" prefix for exact name matching (tmux 3.2+).
	// Without it, "has-session -t foo" matches "foobar" via prefix.
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

func (r *RealExecutor) SendKeys(target, keys string) error {
	return exec.Command("tmux", "send-keys", "-t", target, keys, "Enter").Run()
}

func (r *RealExecutor) SendKeysRaw(target string, keys ...string) error {
	args := []string{"send-keys", "-t", target}
	args = append(args, keys...)
	return exec.Command("tmux", args...).Run()
}

func (r *RealExecutor) PaneCurrentCommand(target string) (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_current_command}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux list-panes: %w", err)
	}
	// Return the first line (active pane's command).
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

func (r *RealExecutor) PanePID(target string) (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_pid}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux list-panes pid: %w", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

func (r *RealExecutor) PaneChildCommands(target string) ([]string, error) {
	panePID, err := r.PanePID(target)
	if err != nil {
		return nil, err
	}
	// ps -ax -o pid,ppid,comm → find children of the pane's shell PID
	out, err := exec.Command("ps", "-ax", "-o", "pid,ppid,comm").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var cmds []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == panePID {
			cmds = append(cmds, fields[2])
		}
	}
	return cmds, nil
}

func (r *RealExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	arg := fmt.Sprintf("-%d", lastN)
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", arg).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

func (r *RealExecutor) PaneSize(target string) (cols, rows int, err error) {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_width} #{pane_height}").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("tmux list-panes size: %w", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	var c, r2 int
	if _, err := fmt.Sscanf(line, "%d %d", &c, &r2); err != nil {
		return 0, 0, fmt.Errorf("parse pane size: %w", err)
	}
	return c, r2, nil
}

func (r *RealExecutor) ResizeWindow(target string, cols, rows int) error {
	return exec.Command("tmux", "resize-window", "-t", target,
		"-x", fmt.Sprintf("%d", cols), "-y", fmt.Sprintf("%d", rows)).Run()
}

func (r *RealExecutor) ResizeWindowAuto(target string) error {
	return exec.Command("tmux", "resize-window", "-A", "-t", target).Run()
}

func (r *RealExecutor) SetWindowOption(target, option, value string) error {
	return exec.Command("tmux", "set-window-option", "-t", target, option, value).Run()
}

func (r *RealExecutor) SetHookGlobal(event, command string) error {
	return exec.Command("tmux", "set-hook", "-g", event, command).Run()
}

func (r *RealExecutor) RemoveHookGlobal(event string) error {
	return exec.Command("tmux", "set-hook", "-gu", event).Run()
}

func (r *RealExecutor) ShowHooksGlobal() (string, error) {
	out, err := exec.Command("tmux", "show-hooks", "-g").Output()
	if err != nil {
		// "no hooks" is a normal condition — return empty string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "no hooks") ||
				strings.Contains(stderr, "no server running") {
				return "", nil
			}
		}
		return "", fmt.Errorf("tmux show-hooks: %w", err)
	}
	return string(out), nil
}

func (r *RealExecutor) TmuxAlive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", "info").Run() == nil
}

