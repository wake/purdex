package facts

import (
	"os"
	"testing"
)

func TestProcIdentOfThisProcessIsStable(t *testing.T) {
	a, ok := ProcIdent(os.Getpid())
	if !ok || a == "" {
		t.Fatalf("own identity: %q %v", a, ok)
	}
	if b, _ := ProcIdent(os.Getpid()); b != a {
		t.Fatalf("identity changed: %q then %q", a, b)
	}
	if _, ok := ProcIdent(os.Getppid()); !ok {
		t.Error("the parent process should have an identity")
	}
	if _, ok := ProcIdent(2147483000); ok {
		t.Error("a pid nobody has must not")
	}
}
