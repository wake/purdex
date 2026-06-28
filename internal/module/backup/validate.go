package backup

import (
	"fmt"
	"strings"
)

// ValidateManifest checks that entries form a valid, canonical snapshot
// manifest (spec §4.2 (b)(c)(e)(f)(g)). It is a pure function — no DB, no blob
// existence (blob existence + size are tx-time checks). Any non-nil error maps
// to HTTP 400. Blob existence (409) and size mismatch (400) are checked later
// in the append transaction.
func ValidateManifest(entries []ManifestEntry) error {
	fileSet := make(map[string]bool, len(entries))

	for i, e := range entries {
		// Canonical order + uniqueness (R2-Pc): sorted by path (byte order),
		// no duplicates. The daemon rejects rather than re-sorts.
		if i > 0 {
			prev := entries[i-1].Path
			if e.Path == prev {
				return fmt.Errorf("duplicate manifest path %q", e.Path)
			}
			if prev > e.Path {
				return fmt.Errorf("manifest not in canonical order: %q before %q", prev, e.Path)
			}
		}

		if err := validatePath(e.Path); err != nil {
			return err
		}

		switch e.Kind {
		case "dir":
			// Directory entries carry no content (R2-Pe).
			if e.Hash != "" || e.Size != 0 || e.Words != 0 {
				return fmt.Errorf("dir entry %q must have empty hash, zero size, zero words", e.Path)
			}
		case "file":
			if !isHex64(e.Hash) {
				return fmt.Errorf("file entry %q hash must be 64-char lowercase hex", e.Path)
			}
			fileSet[e.Path] = true
		default:
			return fmt.Errorf("entry %q has invalid kind %q", e.Path, e.Kind)
		}
	}

	// Well-formed tree (R2-Pb): no file path may be a strict prefix-ancestor of
	// another entry — walk each entry's ancestor chain and reject if any
	// ancestor is a file.
	for _, e := range entries {
		p := e.Path
		for {
			idx := strings.LastIndex(p, "/")
			if idx < 0 {
				break
			}
			p = p[:idx]
			if fileSet[p] {
				return fmt.Errorf("file %q is a prefix-ancestor of %q", p, e.Path)
			}
		}
	}

	return nil
}

// validatePath enforces the root-relative path rules (spec §4.2 (b)): non-empty,
// no leading slash, no backslash, no empty/`.`/`..` segments.
func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("manifest path is empty")
	}
	if strings.Contains(p, "\\") {
		return fmt.Errorf("manifest path %q contains a backslash", p)
	}
	for _, seg := range strings.Split(p, "/") {
		// Empty segment catches leading slash, trailing slash, and `a//b`.
		if seg == "" {
			return fmt.Errorf("manifest path %q has an empty segment", p)
		}
		if seg == "." || seg == ".." {
			return fmt.Errorf("manifest path %q contains a %q segment", p, seg)
		}
	}
	return nil
}

// isHex64 reports whether s is exactly 64 lowercase hexadecimal characters.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
