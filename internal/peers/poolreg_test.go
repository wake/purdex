package peers

import (
	"slices"
	"sync"
	"testing"
)

func TestPool_DefaultIsTheBuiltIn30RecognisedAndPickable(t *testing.T) {
	ResetPool()
	if got := PickablePool(); !slices.Equal(got, NamePool[:]) {
		t.Fatalf("pickable = %v, want the built-in list", got)
	}
	for _, n := range NamePool {
		if _, ok := PoolNameOf("purdex-3f-" + n); !ok {
			t.Errorf("built-in %q is not recognised", n)
		}
	}
	if d, ok := PoolDisplayName("adam"); !ok || d != "Adam" {
		t.Errorf("PoolDisplayName(adam) = %q, %v; want Adam", d, ok)
	}
	if _, ok := PoolDisplayName("hiro"); ok {
		t.Error("hiro is not a built-in")
	}
}

// What SetPool is given is what is recognised and what is picked: a retired persona is recognised (a live holder keeps its
// name, spec L5) and never picked.
// Mutation gate: pick from the recognised set -> a retired code is picked -> red.
func TestPool_RecognisedAndPickableAreSeparate(t *testing.T) {
	t.Cleanup(ResetPool)
	SetPool(map[string]string{"hiro": "Hiro Tanaka", "anna": "Anna"}, []string{"hiro"})
	if got, ok := PoolNameOf("purdex-3f-anna"); !ok || got != "anna" {
		t.Errorf("a retired persona is not recognised: %q %v", got, ok)
	}
	if got := PickablePool(); !slices.Equal(got, []string{"hiro"}) {
		t.Errorf("pickable = %v, want [hiro]", got)
	}
	if d, ok := PoolDisplayName("hiro"); !ok || d != "Hiro Tanaka" {
		t.Errorf("display = %q, %v", d, ok)
	}
	if _, ok := PoolNameOf("purdex-3f-adam"); ok {
		t.Error("a built-in name that SetPool did not list is still recognised")
	}
}

func TestPool_TheCallersSlicesAreCopied(t *testing.T) {
	t.Cleanup(ResetPool)
	rec := map[string]string{"hiro": "Hiro"}
	pick := []string{"hiro"}
	SetPool(rec, pick)
	rec["mia"] = "Mia"
	pick[0] = "mia"
	if _, ok := PoolNameOf("x-mia"); ok {
		t.Error("the registry kept the caller's map")
	}
	if got := PickablePool(); got[0] != "hiro" {
		t.Errorf("the registry kept the caller's slice: %v", got)
	}
	got := PickablePool()
	got[0] = "zzz"
	if PickablePool()[0] != "hiro" {
		t.Error("PickablePool hands out its own slice")
	}
}

func TestPool_ResetRestoresTheBuiltIn(t *testing.T) {
	SetPool(map[string]string{"hiro": "Hiro"}, []string{"hiro"})
	ResetPool()
	if _, ok := PoolNameOf("x-adam"); !ok {
		t.Error("reset did not bring the built-in list back")
	}
}

// The registry is swapped while the naming passes read it: run with -race.
// Mutation gate: swap a plain package variable instead of the atomic pointer -> the race detector -> red.
func TestPool_SwapIsRaceFree(t *testing.T) {
	t.Cleanup(ResetPool)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				SetPool(map[string]string{"hiro": "Hiro"}, []string{"hiro"})
				ResetPool()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				PoolNameOf("x-hiro")
				PickablePool()
				PoolDisplayName("adam")
			}
		}()
	}
	wg.Wait()
}
