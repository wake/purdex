package manifest

import (
	"reflect"
	"testing"
)

func TestAdHocTaskIsTheCallersWordsAsTheWholeArgv(t *testing.T) {
	task := AdHocTask()
	if !task.Local.Permits(0) || !task.Local.Permits(500) {
		t.Fatal("an undeclared command must be able to take the local lane with any number of words")
	}
	argv, err := task.Render([]string{"go", "test", "-run", "X y", "./a"}, RenderEnv{Workspace: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"go", "test", "-run", "X y", "./a"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %q, want %q: the words go through as given, whole", argv, want)
	}
	if task.Name != "adhoc" || len(task.Locks) != 0 || len(task.Labels) != 0 || task.Cacheable || task.Interruptible || task.Env != nil {
		t.Fatalf("an undeclared command declares nothing: %+v", task)
	}
}

func TestAdHocTaskKeepsShellCharactersAsPlainWords(t *testing.T) {
	// no shell: operators are words of the command, they are not interpreted
	argv, err := AdHocTask().Render([]string{"echo", "a|b", "$HOME", ";", "`x`"}, RenderEnv{Workspace: "/w"})
	if err != nil || len(argv) != 5 || argv[1] != "a|b" || argv[2] != "$HOME" {
		t.Fatalf("argv %q err %v", argv, err)
	}
}
