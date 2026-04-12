package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wake/tmux-box/internal/config"
)

func acquirePidLock(pidPath string, pid int) (*os.File, error) {
	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pid file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		data, _ := os.ReadFile(pidPath)
		existingPid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return nil, fmt.Errorf("already running (pid %d)", existingPid)
	}

	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d", pid)
	f.Sync()

	return f, nil
}

func releasePidLock(f *os.File, pidPath string) {
	if f != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
	os.Remove(pidPath)
}

func isDaemonRunning(pidPath string) (bool, int) {
	f, err := os.OpenFile(pidPath, os.O_RDWR, 0644)
	if err != nil {
		return false, 0
	}
	defer f.Close()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		data, _ := os.ReadFile(pidPath)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return true, pid
	}

	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	data, _ := os.ReadFile(pidPath)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return false, pid
}

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

	pidPath := filepath.Join(cfg.DataDir, "tbox.pid")

	if running, pid := isDaemonRunning(pidPath); running {
		fmt.Fprintf(os.Stderr, "tbox: already running (pid %d)\n", pid)
		os.Exit(1)
	}

	logsDir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		log.Fatalf("create logs dir: %v", err)
	}

	logFile, err := os.OpenFile(filepath.Join(logsDir, "tbox.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
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
	healthy := false

	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			healthy = true
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !healthy {
		fmt.Fprintf(os.Stderr, "tbox: daemon started but health check failed, killing child\n")
		cmd.Process.Kill()
		fmt.Fprintf(os.Stderr, "tbox: last 20 lines of %s:\n\n", filepath.Join(logsDir, "tbox.log"))
		tailCmd := exec.Command("tail", "-n", "20", filepath.Join(logsDir, "tbox.log"))
		tailCmd.Stdout = os.Stderr
		tailCmd.Run()
		os.Exit(1)
	}

	logPath := filepath.Join(logsDir, "tbox.log")
	fmt.Printf("tbox daemon started (pid %d, bind %s, log %s)\n", childPid, addr, logPath)
}

func runStop(args []string) {
	cfg, _ := parseConfigPath(args)

	pidPath := filepath.Join(cfg.DataDir, "tbox.pid")

	running, pid := isDaemonRunning(pidPath)
	if !running {
		fmt.Println("tbox: not running")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tbox: cannot find process %d: %v\n", pid, err)
		os.Exit(1)
	}

	proc.Signal(syscall.SIGTERM)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := isDaemonRunning(pidPath); !r {
			fmt.Printf("tbox: stopped (pid %d)\n", pid)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "tbox: daemon did not stop within 30s, sending SIGKILL\n")
	proc.Signal(syscall.SIGKILL)
	time.Sleep(1 * time.Second)
	os.Remove(pidPath)
}

func runStatus(args []string) {
	cfg, _ := parseConfigPath(args)

	pidPath := filepath.Join(cfg.DataDir, "tbox.pid")
	logsDir := filepath.Join(cfg.DataDir, "logs")
	logPath := filepath.Join(logsDir, "tbox.log")

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
