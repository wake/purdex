//go:build !darwin

package deps

import "os"

// cloneAt is not available off macOS: the caller copies with cp (a reflink where the file system has one).
func cloneAt(srcParent, dstParent *os.File, name string) error { return errNoCloneAt }
