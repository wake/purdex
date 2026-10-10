package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/config"
)

// parseConfigPath extracts --config value from args and loads config accordingly.
func parseConfigPath(args []string) (config.Config, string) {
	var cfgPath string
	for i, a := range args {
		if (a == "--config" || a == "-config") && i+1 < len(args) {
			cfgPath = args[i+1]
			break
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	return cfg, cfgPath
}

func runStart(args []string) {
	cfg, _ := parseConfigPath(args)

	pidPath := filepath.Join(cfg.DataDir, "pdx.pid")

	if running, pid := isDaemonRunning(pidPath); running {
		fmt.Fprintf(os.Stderr, "pdx: already running (pid %d)\n", pid)
		os.Exit(1)
	}

	logsDir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		log.Fatalf("create logs dir: %v", err)
	}

	logFile, err := os.OpenFile(filepath.Join(logsDir, "pdx.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}

	resolvedArgs := make([]string, len(args))
	copy(resolvedArgs, args)
	for i, a := range resolvedArgs {
		if (a == "--config" || a == "-config") && i+1 < len(resolvedArgs) {
			abs, err := filepath.Abs(resolvedArgs[i+1])
			if err == nil {
				resolvedArgs[i+1] = abs
			}
		}
	}

	self, err := os.Executable()
	if err != nil {
		log.Fatalf("os.Executable: %v", err)
	}

	cmd := exec.Command(self, append([]string{"serve"}, resolvedArgs...)...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		log.Fatalf("start daemon: %v", err)
	}
	logFile.Close()

	childPid := cmd.Process.Pid

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	healthURL := fmt.Sprintf("http://%s/api/health", addr)

	// Buffered so this goroutine never blocks, even when nobody reads the result.
	childExited := make(chan error, 1)
	go func() {
		childExited <- cmd.Wait()
	}()

	notify := func(msg string) {
		fmt.Fprintf(os.Stderr, "pdx: %s\n", msg)
	}

	if err := waitForHealthy(healthURL, childPid, childExited, healthCheckTimeout, healthCheckInterval, notify); err != nil {
		fmt.Fprintf(os.Stderr, "pdx: %v\n", err)
		if kerr := killProcessGroup(childPid); kerr != nil {
			fmt.Fprintf(os.Stderr, "pdx: kill process group %d: %v\n", childPid, kerr)
		}
		fmt.Fprintf(os.Stderr, "pdx: last 20 lines of %s:\n\n", filepath.Join(logsDir, "pdx.log"))
		tailCmd := exec.Command("tail", "-n", "20", filepath.Join(logsDir, "pdx.log"))
		tailCmd.Stdout = os.Stderr
		tailCmd.Run()
		os.Exit(1)
	}

	logPath := filepath.Join(logsDir, "pdx.log")
	fmt.Printf("pdx daemon started (pid %d, bind %s, log %s)\n", childPid, addr, logPath)
}

func runStop(args []string) {
	cfg, _ := parseConfigPath(args)

	pidPath := filepath.Join(cfg.DataDir, "pdx.pid")

	running, pid := isDaemonRunning(pidPath)
	if !running {
		fmt.Println("pdx: not running")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pdx: cannot find process %d: %v\n", pid, err)
		os.Exit(1)
	}

	proc.Signal(syscall.SIGTERM)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := isDaemonRunning(pidPath); !r {
			fmt.Printf("pdx: stopped (pid %d)\n", pid)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "pdx: daemon did not stop within 30s, sending SIGKILL\n")
	proc.Signal(syscall.SIGKILL)
	time.Sleep(1 * time.Second)
	// The pid file is permanent (see releasePidLock); the kernel drops the
	// flock when the killed process exits, so no unlink is needed here.
}

func runStatus(args []string) {
	cfg, _ := parseConfigPath(args)

	pidPath := filepath.Join(cfg.DataDir, "pdx.pid")
	logsDir := filepath.Join(cfg.DataDir, "logs")
	logPath := filepath.Join(logsDir, "pdx.log")

	running, pid := isDaemonRunning(pidPath)
	if !running {
		fmt.Println("Status:  stopped")
		os.Exit(1)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	healthURL := fmt.Sprintf("http://%s/api/health", addr)

	health := "unreachable"
	resp, err := http.Get(healthURL)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			health = "ok"
		} else {
			health = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}

	fmt.Printf("Status:  running\n")
	fmt.Printf("PID:     %d\n", pid)
	fmt.Printf("Bind:    %s\n", addr)
	fmt.Printf("Health:  %s\n", health)
	fmt.Printf("Log:     %s\n", logPath)
}
