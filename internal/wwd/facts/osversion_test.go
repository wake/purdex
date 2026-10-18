package facts

import (
	"regexp"
	"testing"
)

func TestOSVersionLooksLikeAVersion(t *testing.T) {
	if v := OSVersion(); !regexp.MustCompile(`^\d+(\.\d+)*$`).MatchString(v) {
		t.Fatalf("os version %q", v)
	}
}
