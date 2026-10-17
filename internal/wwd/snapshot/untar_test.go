package snapshot

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarOf builds a tar from entries; a Linkname makes a symlink, a trailing / a directory.
type ent struct {
	name, body, link string
	mode             int64
	typ              byte
}

func tarOf(t *testing.T, es ...ent) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range es {
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.mode == 0 {
			h.Mode = 0o644
		}
		switch {
		case e.typ != 0:
			h.Typeflag = e.typ
			h.Size = 0
			h.Linkname = e.link
		case e.link != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		case len(e.name) > 0 && e.name[len(e.name)-1] == '/':
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		}
		require.NoError(t, tw.WriteHeader(h))
		if h.Size > 0 {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return bytes.NewReader(buf.Bytes())
}

func TestUntar_FilesDirectoriesModesAndSymlinksInside(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "ws")
	_, err := Untar(tarOf(t,
		ent{name: "a.txt", body: "a\n"},
		ent{name: "sub/", mode: 0o755},
		ent{name: "sub/run.sh", body: "#!/bin/sh\n", mode: 0o755},
		ent{name: "sub/deep/b.txt", body: "b\n"}, // the directory is made when the entry comes first
		ent{name: "link", link: "sub/run.sh"},    // a symlink that stays inside
	), dst)
	require.NoError(t, err)
	b, err := os.ReadFile(dst + "/sub/deep/b.txt")
	require.NoError(t, err)
	assert.Equal(t, "b\n", string(b))
	st, err := os.Stat(dst + "/sub/run.sh")
	require.NoError(t, err)
	assert.NotZero(t, st.Mode()&0o100, "the executable bit survives")
	target, err := os.Readlink(dst + "/link")
	require.NoError(t, err)
	assert.Equal(t, "sub/run.sh", target)
}

// What a tar must not be able to do: write outside the workspace by a path, by an absolute path, or through a symlink it made itself.
func TestUntar_RefusesWhatEscapesTheWorkspace(t *testing.T) {
	cases := map[string][]ent{
		"dot-dot":                 {{name: "../evil.txt", body: "x"}},
		"dot-dot inside":          {{name: "a/../../evil.txt", body: "x"}},
		"absolute":                {{name: "/tmp/evil-wwd.txt", body: "x"}},
		"symlink out then write":  {{name: "esc", link: "../outside"}, {name: "esc/evil.txt", body: "x"}},
		"symlink to absolute":     {{name: "esc", link: "/etc"}},
		"symlink to dot-dot":      {{name: "esc", link: "../../etc"}},
		"hardlink":                {{name: "h", typ: tar.TypeLink, link: "../x"}},
		"device":                  {{name: "dev", typ: tar.TypeChar}},
		"fifo":                    {{name: "ff", typ: tar.TypeFifo}},
		"symlink in a dir to out": {{name: "d/", mode: 0o755}, {name: "d/l", link: "../../x"}},
	}
	for name, es := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dst := filepath.Join(root, "ws")
			_, err := Untar(tarOf(t, es...), dst)
			assert.Error(t, err, "refused")
			for _, p := range []string{"evil.txt", "outside", "x"} {
				assert.NoFileExists(t, filepath.Join(root, p))
			}
			assert.NoFileExists(t, "/tmp/evil-wwd.txt")
		})
	}
}

// git archive puts a pax global header (the commit id) first: it is not a file.
func TestUntar_TheGlobalHeaderOfGitArchiveIsSkipped(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "0123456789abcdef0123456789abcdef01234567"}}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "f.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte("f"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	dst := filepath.Join(t.TempDir(), "ws")
	commit, err := Untar(&buf, dst)
	require.NoError(t, err)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", commit, "the commit id of git archive is returned")
	assert.FileExists(t, dst+"/f.txt")
	assert.NoFileExists(t, dst+"/pax_global_header")
}

// A file in the way of a directory (or the reverse) is an error, not a silent overwrite of what an earlier entry made.
func TestUntar_ADuplicateOfADifferentKindIsRefused(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "ws")
	_, err := Untar(tarOf(t, ent{name: "x", body: "file"}, ent{name: "x/", mode: 0o755}), dst)
	assert.Error(t, err)
}
