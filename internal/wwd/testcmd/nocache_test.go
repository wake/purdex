package testcmd

import "testing"

func TestNoCacheFlag(t *testing.T) {
	a, err := ParseArgs([]string{"--no-cache", "lint", "--detach"})
	if err != nil || !a.NoCache || a.Task != "lint" || !a.Detach {
		t.Fatalf("%+v %v", a, err)
	}
	a, err = ParseArgs([]string{"lint", "--", "--no-cache"})
	if err != nil || a.NoCache || len(a.TaskArgs) != 1 {
		t.Fatalf("after --, it is an argument of the task: %+v %v", a, err)
	}
	for _, argv := range [][]string{{"--no-cache", "--list"}, {"--no-cache", "--adhoc", "--", "ls"}} {
		if _, err := ParseArgs(argv); err == nil {
			t.Errorf("%v should be refused: there is no cache to skip", argv)
		}
	}
}
