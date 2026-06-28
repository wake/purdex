package backup

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func hex64(c byte) string { return strings.Repeat(string(c), 64) }

func TestValidateManifest(t *testing.T) {
	h := hex64('a')
	h2 := hex64('b')

	tests := []struct {
		name    string
		entries []ManifestEntry
		wantErr bool
	}{
		{
			name: "ok canonical file+dir tree",
			entries: []ManifestEntry{
				{Path: "a", Kind: "dir"},
				{Path: "a/b.txt", Kind: "file", Hash: h, Size: 3, Words: 1},
				{Path: "c.txt", Kind: "file", Hash: h2, Size: 5, Words: 2},
				{Path: "empty-dir", Kind: "dir"},
			},
			wantErr: false,
		},
		{
			name:    "ok empty manifest",
			entries: []ManifestEntry{},
			wantErr: false,
		},
		{
			name: "unsorted",
			entries: []ManifestEntry{
				{Path: "b.txt", Kind: "file", Hash: h, Size: 1},
				{Path: "a.txt", Kind: "file", Hash: h2, Size: 1},
			},
			wantErr: true,
		},
		{
			name: "duplicate path",
			entries: []ManifestEntry{
				{Path: "a.txt", Kind: "file", Hash: h, Size: 1},
				{Path: "a.txt", Kind: "file", Hash: h2, Size: 1},
			},
			wantErr: true,
		},
		{
			name:    "empty path",
			entries: []ManifestEntry{{Path: "", Kind: "dir"}},
			wantErr: true,
		},
		{
			name:    "dotdot segment",
			entries: []ManifestEntry{{Path: "a/../b", Kind: "dir"}},
			wantErr: true,
		},
		{
			name:    "dot alone",
			entries: []ManifestEntry{{Path: ".", Kind: "dir"}},
			wantErr: true,
		},
		{
			name:    "dot segment a/./b",
			entries: []ManifestEntry{{Path: "a/./b", Kind: "dir"}},
			wantErr: true,
		},
		{
			name:    "leading slash",
			entries: []ManifestEntry{{Path: "/a", Kind: "dir"}},
			wantErr: true,
		},
		{
			name:    "backslash",
			entries: []ManifestEntry{{Path: "a\\b", Kind: "dir"}},
			wantErr: true,
		},
		{
			name:    "empty segment double slash",
			entries: []ManifestEntry{{Path: "a//b", Kind: "dir"}},
			wantErr: true,
		},
		{
			name:    "bad kind",
			entries: []ManifestEntry{{Path: "a", Kind: "symlink"}},
			wantErr: true,
		},
		{
			name:    "dir with non-empty hash",
			entries: []ManifestEntry{{Path: "a", Kind: "dir", Hash: h}},
			wantErr: true,
		},
		{
			name:    "dir with non-zero size",
			entries: []ManifestEntry{{Path: "a", Kind: "dir", Size: 4}},
			wantErr: true,
		},
		{
			name:    "dir with non-zero words",
			entries: []ManifestEntry{{Path: "a", Kind: "dir", Words: 7}},
			wantErr: true,
		},
		{
			name:    "file hash not 64-hex (short)",
			entries: []ManifestEntry{{Path: "a", Kind: "file", Hash: "abc", Size: 1}},
			wantErr: true,
		},
		{
			name:    "file hash not hex (uppercase)",
			entries: []ManifestEntry{{Path: "a", Kind: "file", Hash: strings.Repeat("A", 64), Size: 1}},
			wantErr: true,
		},
		{
			name:    "file hash not hex (non-hex char)",
			entries: []ManifestEntry{{Path: "a", Kind: "file", Hash: strings.Repeat("g", 64), Size: 1}},
			wantErr: true,
		},
		{
			name: "prefix conflict file is ancestor",
			entries: []ManifestEntry{
				{Path: "a", Kind: "file", Hash: h, Size: 1},
				{Path: "a/b", Kind: "file", Hash: h2, Size: 1},
			},
			wantErr: true,
		},
		{
			name: "file ancestor of dir",
			entries: []ManifestEntry{
				{Path: "a", Kind: "file", Hash: h, Size: 1},
				{Path: "a/b", Kind: "dir"},
			},
			wantErr: true,
		},
		{
			name: "dir ancestor of file is OK",
			entries: []ManifestEntry{
				{Path: "a", Kind: "dir"},
				{Path: "a/b", Kind: "file", Hash: h, Size: 1},
			},
			wantErr: false,
		},
		{
			name: "implicit ancestor dir is OK",
			entries: []ManifestEntry{
				{Path: "a/b/c.txt", Kind: "file", Hash: h, Size: 1},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateManifest(tt.entries)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
