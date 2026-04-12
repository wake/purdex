package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

var (
	redactMu     sync.RWMutex
	redactTokens []string
)

func setRedactTokens(tokens []string) {
	redactMu.Lock()
	defer redactMu.Unlock()
	redactTokens = tokens
}

var (
	reAuthHeader = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)\S+`)
	rePurdexTok  = regexp.MustCompile(`(?i)(purdex_|tbox_)\S+`)
)

func redactSecrets(s string) string {
	s = reAuthHeader.ReplaceAllString(s, "${1}[REDACTED]")
	s = rePurdexTok.ReplaceAllString(s, "[REDACTED]")

	redactMu.RLock()
	tokens := redactTokens
	redactMu.RUnlock()

	for _, tok := range tokens {
		if tok != "" {
			s = strings.ReplaceAll(s, tok, "[REDACTED]")
		}
	}
	return s
}

func writeCrashLog(logsDir string, panicVal interface{}, stack []byte) {
	os.MkdirAll(logsDir, 0700)

	ts := time.Now().Format("20060102-150405")
	path := filepath.Join(logsDir, fmt.Sprintf("crash-%s.log", ts))

	bi, _ := debug.ReadBuildInfo()
	goVersion := runtime.Version()
	version := "unknown"
	if bi != nil && bi.Main.Version != "" {
		version = bi.Main.Version
	}
	// Best-effort: try reading VERSION from executable's directory (repo root)
	if self, err := os.Executable(); err == nil {
		if vData, err := os.ReadFile(filepath.Join(filepath.Dir(self), "VERSION")); err == nil {
			version = strings.TrimSpace(string(vData))
		}
	}

	content := fmt.Sprintf("Time:        %s\nVersion:     %s\nGo Runtime:  %s\nGoroutines:  %d\n\nPanic: %v\n\nStack:\n%s\n",
		time.Now().Format(time.RFC3339),
		version,
		goVersion,
		runtime.NumGoroutine(),
		panicVal,
		string(stack),
	)

	content = redactSecrets(content)
	os.WriteFile(path, []byte(content), 0600)
}
