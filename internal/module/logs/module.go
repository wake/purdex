package logs

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/wake/tmux-box/internal/core"
)

type LogsModule struct {
	logsDir string
}

func New() *LogsModule {
	return &LogsModule{}
}

func (m *LogsModule) Name() string           { return "logs" }
func (m *LogsModule) Dependencies() []string { return nil }

func (m *LogsModule) Init(c *core.Core) error {
	m.logsDir = filepath.Join(c.Cfg.DataDir, "logs")
	return nil
}

func (m *LogsModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs/daemon", m.handleDaemonLog)
	mux.HandleFunc("GET /api/logs/crash", m.handleCrashLog)
}

func (m *LogsModule) Start(_ context.Context) error {
	log.Println("[logs] endpoints enabled")
	return nil
}

func (m *LogsModule) Stop(_ context.Context) error { return nil }

var reCrashFile = regexp.MustCompile(`^crash-\d{8}-\d{6}\.log$`)

func (m *LogsModule) handleDaemonLog(w http.ResponseWriter, r *http.Request) {
	logPath := filepath.Join(m.logsDir, "tbox.log")
	if _, err := os.Stat(logPath); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	tailN := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 2000 {
				n = 2000
			}
			tailN = n
		}
	}

	cmd := exec.Command("tail", "-n", strconv.Itoa(tailN), logPath)
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, "failed to read log", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}

func (m *LogsModule) handleCrashLog(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(m.logsDir)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var crashFiles []string
	for _, e := range entries {
		name := e.Name()
		cleaned := filepath.Base(filepath.Clean(name))
		if cleaned != name {
			continue
		}
		if reCrashFile.MatchString(name) {
			crashFiles = append(crashFiles, name)
		}
	}

	if len(crashFiles) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	sort.Strings(crashFiles)
	latest := crashFiles[len(crashFiles)-1]

	fullPath := filepath.Join(m.logsDir, latest)
	if !strings.HasPrefix(fullPath, m.logsDir) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "failed to read crash log", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}
