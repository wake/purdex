package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProfile(t *testing.T) {
	cases := map[string]Profile{
		"read-only":       ProfileReadOnly,
		"ask":             ProfileAsk,
		"workspace-write": ProfileWorkspaceWrite,
		"danger-full":     ProfileDangerFull,
	}
	for in, want := range cases {
		got, err := ParseProfile(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
		// round-trip through String().
		require.Equal(t, in, got.String(), in)
	}
}

func TestParseProfile_Unknown(t *testing.T) {
	for _, in := range []string{"yolo-mode", "", "READ-ONLY", "full", " ask "} {
		_, err := ParseProfile(in)
		require.ErrorIs(t, err, ErrUnknownProfile, in)
	}
}

func TestProfile_TotalOrder(t *testing.T) {
	// Strictest → widest, encoded by iota so min = stricter = smaller.
	require.Less(t, int(ProfileReadOnly), int(ProfileAsk))
	require.Less(t, int(ProfileAsk), int(ProfileWorkspaceWrite))
	require.Less(t, int(ProfileWorkspaceWrite), int(ProfileDangerFull))
}

func TestProfile_ToPermissionMode(t *testing.T) {
	require.Equal(t, "plan", ProfileReadOnly.ToPermissionMode())
	require.Equal(t, "default", ProfileAsk.ToPermissionMode())
	require.Equal(t, "acceptEdits", ProfileWorkspaceWrite.ToPermissionMode())
	require.Equal(t, "bypassPermissions", ProfileDangerFull.ToPermissionMode())
}

// TestClamp_Cases mirrors docs/fixtures/m0/sandbox.clamp.json exactly.
func TestClamp_Cases(t *testing.T) {
	cases := []struct {
		name       string
		request    string
		hostPolicy Profile // already-resolved (null host → ask)
		want       Profile
	}{
		{"request wider → clamp to host", "danger-full", ProfileWorkspaceWrite, ProfileWorkspaceWrite},
		{"request stricter → keep request", "read-only", ProfileWorkspaceWrite, ProfileReadOnly},
		{"request omitted → host default", "", ProfileAsk, ProfileAsk},
		{"host unset (→ask) narrows request", "workspace-write", DefaultHostPolicy, ProfileAsk},
		{"equal → same", "ask", ProfileAsk, ProfileAsk},
	}
	for _, tc := range cases {
		got, err := Clamp(tc.request, tc.hostPolicy)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}
}

func TestClamp_UnknownRequest(t *testing.T) {
	_, err := Clamp("yolo-mode", ProfileDangerFull)
	require.ErrorIs(t, err, ErrUnknownProfile)
}

func TestResolveHostPolicy(t *testing.T) {
	got, err := ResolveHostPolicy("")
	require.NoError(t, err)
	require.Equal(t, DefaultHostPolicy, got)
	require.Equal(t, ProfileAsk, got, "unset host → least-privilege ask, not danger-full")

	got, err = ResolveHostPolicy("workspace-write")
	require.NoError(t, err)
	require.Equal(t, ProfileWorkspaceWrite, got)

	_, err = ResolveHostPolicy("nonsense")
	require.ErrorIs(t, err, ErrUnknownProfile)
}
