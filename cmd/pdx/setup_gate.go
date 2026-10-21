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

// retireGateForUser is the gate half of `pdx setup` since WWD-9 S0-b: the gate is a pass-through, so no shim is installed and the pdx gate block
// comes out of ~/.zshenv (the shims already in ~/.config/pdx/shims stay: the shells that started before still have them first on PATH, and they
// are harmless now; 9a-1 removes the directory). It never fails the setup: the agent hooks are installed already, and what could not be done is
// a line on w.
func retireGateForUser(w io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(w, "setup: could not remove the pdx gate block: %v\n", err)
		return
	}
	retireGate(home, w)
}

func retireGate(home string, w io.Writer) {
	switch out, err := shim.RemoveBlock(home); {
	case err != nil:
		fmt.Fprintf(w, "setup: could not remove the pdx gate block: %v\n", err)
	case out == shim.BlockRemoved:
		fmt.Fprintln(w, "pdx gate ~/.zshenv block removed")
	case out == shim.BlockNotOurs:
		fmt.Fprintln(w, "setup: ~/.zshenv has text between the pdx gate markers that pdx did not write; left as it is (remove the block by hand, from the line \"# >>> pdx gate >>>\")")
	case out == shim.BlockUnclosed:
		fmt.Fprintln(w, "setup: ~/.zshenv has a pdx gate block with no end marker; left as it is (remove it by hand, from the line \"# >>> pdx gate >>>\")")
	}
}
