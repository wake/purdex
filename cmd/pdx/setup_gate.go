package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/gate/shim"
)

// setupGateShims is the gate half of `pdx setup` (spec 2026-10-15 §4): the shims in ~/.config/pdx/shims and the delimited
// ~/.zshenv block that puts them first on PATH. It runs after the agent hooks are installed and never fails the setup: a
// home that cannot be written is said on w and the hooks stay installed. pdxPath is the pdx the shims call.
func setupGateShims(home, pdxPath string, w io.Writer) {
	if err := shim.Install(home, pdxPath); err != nil {
		fmt.Fprintf(w, "setup: gate shims not installed: %v\n", err)
		return
	}
	fmt.Fprintf(w, "pdx gate shims installed in %s\n", shim.DirIn(home))
}

// uninstallGateShims is `pdx setup --uninstall-gate`: the ~/.zshenv block (and nothing else in that file) and the shims.
func uninstallGateShims(home string, w io.Writer) error {
	if err := shim.Uninstall(home); err != nil {
		return err
	}
	fmt.Fprintln(w, "pdx gate shims removed")
	return nil
}

// thisPdx is the path of the running pdx, symlinks followed, as localSetup finds it.
func thisPdx() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find pdx binary: %w", err)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// setupGateForUser runs setupGateShims for the current user.
func setupGateForUser(w io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(w, "setup: gate shims not installed: %v\n", err)
		return
	}
	pdxPath, err := thisPdx()
	if err != nil {
		fmt.Fprintf(w, "setup: gate shims not installed: %v\n", err)
		return
	}
	setupGateShims(home, pdxPath, w)
}
