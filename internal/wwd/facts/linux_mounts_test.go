package facts

import "testing"

func TestMountOfFindsTheFileSystemHoldingAPath(t *testing.T) {
	points := ParseMountPoints("22 1 8:1 / / rw - ext4 /dev/sda1 rw\n40 22 8:2 / /home rw - ext4 /dev/sda2 rw\n41 22 8:3 / /srv/my\\040data rw - ext4 /dev/sda3 rw\nshort\n")
	for path, want := range map[string]string{
		"/":              "/",
		"/home/wake":     "/home",
		"/home":          "/home",
		"/homework":      "/",
		"/srv/my data/x": "/srv/my data",
		"/var/lib":       "/",
	} {
		if got := MountOf(points, path); got != want {
			t.Errorf("MountOf(%q) = %q, want %q", path, got, want)
		}
	}
	if got := MountOf(nil, "/home/wake"); got != "/" {
		t.Errorf("without mountinfo the root is the answer: %q", got)
	}
}
