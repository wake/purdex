package facts

import "testing"

func TestFreeGBReadsTheVolumeOfThePath(t *testing.T) {
	got, err := FreeGB(t.TempDir())
	if err != nil || got <= 0 {
		t.Fatalf("free space of a temp dir: %v %v", got, err)
	}
	if _, err := FreeGB("/definitely/not/a/path/xyz"); err == nil {
		t.Fatal("a path that is not there was read")
	}
}
