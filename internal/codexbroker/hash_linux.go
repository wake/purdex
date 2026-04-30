//go:build linux

package codexbroker

// IsCaseInsensitiveVolume returns false on Linux: default ext4 / xfs / btrfs
// volumes are case-sensitive. Linux does support case-insensitive directories
// (ext4 +F, ntfs-3g, etc.) but they require explicit opt-in and are not the
// default; defending against them requires a per-path query via statx
// (STATX_ATTR_CASEFOLD), which is out of scope for P1.
func IsCaseInsensitiveVolume(path string) bool {
	return false
}
