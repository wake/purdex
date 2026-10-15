package gate

import (
	"context"
	"os"
	"testing"
)

// holdSlot starts a command that holds a slot of the class until release is called: it is let go by a file, not by the
// clock, so a loaded host cannot end the hold before the test has looked (#2936). release is also run when the test ends.
func holdSlot(t *testing.T, g *Gate, class Class, name string) (done <-chan runResult, release func()) {
	t.Helper()
	dir := t.TempDir()
	done = runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, name)), Class: class})
	released := false
	release = func() {
		if !released {
			released = true
			_ = os.WriteFile(dir+"/go-"+name, nil, 0o644)
		}
	}
	t.Cleanup(release)
	eventually(t, "the held command to start", func() bool { return exists(dir + "/started-" + name) })
	return done, release
}
