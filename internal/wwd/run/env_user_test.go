package run

import (
	"testing"
)

func withUser(t *testing.T, name string) {
	t.Helper()
	old := currentUser
	currentUser = func() string { return name }
	t.Cleanup(func() { currentUser = old })
}

func TestBuildEnvCarriesTheExecutingAccount(t *testing.T) {
	withUser(t, "runner-acct")
	env, err := BuildEnv("j_a1", "", "/ws", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"USER", "LOGNAME"} {
		if v, ok := envValue(env, k); !ok || v != "runner-acct" {
			t.Errorf("%s = %q (%v), want runner-acct", k, v, ok)
		}
	}
}

func TestTaskEnvCannotSetUserOrLogname(t *testing.T) {
	withUser(t, "runner-acct")
	for _, e := range []string{"USER=x", "LOGNAME=x", "USER", "LOGNAME"} {
		if _, err := BuildEnv("j_a1", "", "/ws", []string{e}, []string{"USER=p", "LOGNAME=p"}); err == nil {
			t.Errorf("%s was accepted", e)
		}
	}
}

func TestBuildEnvWithoutAnAccountNameSetsNeither(t *testing.T) {
	withUser(t, "")
	env, err := BuildEnv("j_a1", "", "/ws", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"USER", "LOGNAME"} {
		if _, ok := envValue(env, k); ok {
			t.Errorf("%s is set without an account name", k)
		}
	}
}
