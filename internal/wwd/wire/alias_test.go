package wire

import (
	"strings"
	"testing"
)

// One rule for a host alias, shared by wwd's config and the Purdex daemon's pool routes: whatever the config accepts, the
// pool can name.
func TestValidAlias(t *testing.T) {
	for _, ok := range []string{"mlab", "air26", "-air 26&host=x/yé", "a..b", "..", strings.Repeat("x", MaxAliasBytes)} {
		if !ValidAlias(ok) {
			t.Errorf("%q should be a valid alias", ok)
		}
	}
	for _, bad := range []string{"", "a\nb", "a\x00b", "a\x7fb", "a\u0085b", "a\u009fb", "\xff\xfe", strings.Repeat("x", MaxAliasBytes+1)} {
		if ValidAlias(bad) {
			t.Errorf("%q should not be a valid alias", bad)
		}
	}
}
