package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/journal"
)

const journalUsage = "usage: pdx journal path [--config <path>]\n" +
	"       prints the folder where the daemon keeps the current directory's project journal (<worktree>/.purdex);\n" +
	"       the lead agent edits roadmap.md there, and the daemon commits it with the rest\n"

func runJournal(args []string) {
	os.Exit(runJournalCmd(context.Background(), args, os.Getwd, os.Stdout, os.Stderr))
}

// runJournalCmd is `pdx journal path`: the `.purdex` folder of the journal worktree of the project the current directory
// belongs to (every worktree of one repository is one project). The folder is made by the daemon on the project's first
// journal write, so before that there is none and the answer is an error, not a made-up path. It reads the config file for
// the data directory and asks git, nothing else: it works without the daemon.
func runJournalCmd(ctx context.Context, args []string, getwd func() (string, error), stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "path" {
		fmt.Fprint(stderr, journalUsage)
		return 2
	}
	fs := flag.NewFlagSet("journal path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "path to config.toml (default: ~/.config/pdx/config.toml)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, journalUsage)
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx journal: config: %v\n", err)
		return 1
	}
	cwd, err := getwd()
	if err != nil {
		fmt.Fprintf(stderr, "pdx journal: cannot read the current directory: %v\n", err)
		return 1
	}
	p, err := journal.ProjectOf(ctx, cwd)
	if err != nil {
		fmt.Fprintf(stderr, "pdx journal: cannot resolve the project of %s: %v\n", cwd, err)
		return 1
	}
	dir := filepath.Join(journal.WorktreeDir(filepath.Join(cfg.DataDir, "journal"), p), ".purdex")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "pdx journal: no journal for this project yet (the daemon makes %s on its first journal write)\n", dir)
		return 1
	}
	fmt.Fprintln(stdout, dir)
	return 0
}
