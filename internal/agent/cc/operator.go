package cc

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

func (p *Provider) Interrupt(ctx context.Context, tmuxTarget string) error {
	tx := p.tmuxExec
	if err := tx.SendKeysRaw(tmuxTarget, "C-u"); err != nil {
		return fmt.Errorf("send C-u: %w", err)
	}
	if err := tx.SendKeysRaw(tmuxTarget, "C-c"); err != nil {
		return fmt.Errorf("send C-c: %w", err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			result, ok := p.prober.CheckReadiness("cc", tmuxTarget)
			if ok && result.Status == agent.StatusIdle {
				return nil
			}
		}
	}
}

func (p *Provider) Exit(ctx context.Context, tmuxTarget string) error {
	tx := p.tmuxExec
	if err := tx.SendKeysRaw(tmuxTarget, "-X", "cancel"); err != nil {
		log.Printf("cc: Exit pane-prep cancel (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "Escape"); err != nil {
		log.Printf("cc: Exit pane-prep Escape (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "C-c"); err != nil {
		log.Printf("cc: Exit pane-prep C-c (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "Escape"); err != nil {
		log.Printf("cc: Exit pane-prep Escape2 (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeys(tmuxTarget, "/exit"); err != nil {
		return fmt.Errorf("send /exit: %w", err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !p.prober.IsAliveFor("cc", tmuxTarget) {
				return nil
			}
		}
	}
}

func (p *Provider) GetStatus(ctx context.Context, tmuxTarget string) (*StatusInfo, error) {
	tx := p.tmuxExec
	didManualResize := false
	if cols, rows, err := tx.PaneSize(tmuxTarget); err == nil && (cols < 80 || rows < 24) {
		if err := tx.ResizeWindow(tmuxTarget, 80, 24); err != nil {
			return nil, fmt.Errorf("resize pane: %w", err)
		}
		didManualResize = true
		sleepCtx(ctx, 200*time.Millisecond)
	}
	if didManualResize {
		p.cfgMu.RLock()
		sizingMode := "latest"
		if p.cfg.Terminal.GetSizingMode() == "minimal-first" {
			sizingMode = "smallest"
		}
		p.cfgMu.RUnlock()
		defer restoreWindowSizing(tx, tmuxTarget, sizingMode)
	}
	if err := tx.SendKeysRaw(tmuxTarget, "-l", "/"); err != nil {
		return nil, fmt.Errorf("send /: %w", err)
	}
	sleepCtx(ctx, 1*time.Second)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "-l", "status"); err != nil {
		return nil, fmt.Errorf("send status: %w", err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "Enter"); err != nil {
		return nil, fmt.Errorf("send Enter: %w", err)
	}
	var statusInfo StatusInfo
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		sleepCtx(ctx, 500*time.Millisecond)
		if ctx.Err() != nil {
			break
		}
		paneContent, err := tx.CapturePaneContent(tmuxTarget, 200)
		if err != nil {
			lastErr = err
			continue
		}
		info, err := ExtractStatusInfo(paneContent)
		if err == nil {
			statusInfo = info
			break
		}
		lastErr = err
	}
	if statusInfo.SessionID == "" {
		if lastErr != nil {
			return nil, fmt.Errorf("could not extract session ID: %w", lastErr)
		}
		return nil, fmt.Errorf("could not extract session ID")
	}
	return &statusInfo, nil
}

func (p *Provider) Launch(ctx context.Context, tmuxTarget string, cmd string) error {
	return p.tmuxExec.SendKeys(tmuxTarget, cmd)
}

func restoreWindowSizing(tx tmux.Executor, target, windowSizeMode string) {
	if err := tx.ResizeWindowAuto(target); err != nil {
		log.Printf("restoreWindowSizing: ResizeWindowAuto(%s): %v", target, err)
	}
	if err := tx.SetWindowOption(target, "window-size", windowSizeMode); err != nil {
		log.Printf("restoreWindowSizing: SetWindowOption(%s): %v", target, err)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
