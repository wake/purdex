package monitor

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const tmuxPaneListFormat = "#{session_id}\t#{session_name}\t#{pane_id}\t#{pane_pid}"

type TmuxCommandRunner interface {
	Output(ctx context.Context, args ...string) (string, error)
}

type tmuxPaneLister struct {
	runner TmuxCommandRunner
}

func NewTmuxPaneLister(runner TmuxCommandRunner) TmuxPaneLister {
	if runner == nil {
		runner = tmuxCLICommandRunner{}
	}
	return &tmuxPaneLister{runner: runner}
}

func (l *tmuxPaneLister) ListPanes(ctx context.Context) ([]TmuxPane, error) {
	out, err := l.runner.Output(ctx, "list-panes", "-a", "-F", tmuxPaneListFormat)
	if err != nil {
		if isNoTmuxPanesOutput(out) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}

	panes, err := parseTmuxPaneListOutput(out)
	if err != nil {
		return nil, err
	}
	return panes, nil
}

func parseTmuxPaneListOutput(out string) ([]TmuxPane, error) {
	var panes []TmuxPane
	for i, line := range strings.Split(out, "\n") {
		lineNo := i + 1
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("malformed tmux pane line %d: expected 4 fields, got %d", lineNo, len(parts))
		}

		panePID, err := strconv.Atoi(parts[3])
		if err != nil || panePID <= 0 {
			return nil, fmt.Errorf("invalid tmux pane pid on line %d: %q", lineNo, parts[3])
		}

		panes = append(panes, TmuxPane{
			TmuxSessionID:   parts[0],
			TmuxSessionName: parts[1],
			PaneID:          parts[2],
			PanePID:         panePID,
		})
	}
	return panes, nil
}

func isNoTmuxPanesOutput(out string) bool {
	return strings.Contains(out, "no server running") || strings.Contains(out, "no sessions")
}

type tmuxCLICommandRunner struct{}

func (tmuxCLICommandRunner) Output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
