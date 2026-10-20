package daemon

import (
	"strings"
	"testing"
)

func TestConfigManifestRefs(t *testing.T) {
	c, err := loadCfg(t, "[repos]\npurdex = \"/srv/purdex\"\n\n[manifest_refs]\npurdex = \"origin/main\"\n")
	if err != nil || c.ManifestRefs["purdex"] != "origin/main" {
		t.Fatalf("a manifest_ref of a configured repo is kept: %v %v", c.ManifestRefs, err)
	}
	if c, err := loadCfg(t, "[repos]\npurdex = \"/srv/purdex\"\n"); err != nil || len(c.ManifestRefs) != 0 {
		t.Fatalf("no [manifest_refs] is fine (the working tree is read as before): %v %v", c.ManifestRefs, err)
	}
	for _, body := range []string{
		"[manifest_refs]\npurdex = \"origin/main\"\n",                                        // no such repo in [repos]
		"[repos]\npurdex = \"/srv/purdex\"\n[manifest_refs]\npurdex = \"\"\n",                // empty
		"[repos]\npurdex = \"/srv/purdex\"\n[manifest_refs]\npurdex = \"--upload-pack=x\"\n", // an option, not a ref
		"[repos]\npurdex = \"/srv/purdex\"\n[manifest_refs]\npurdex = \"a b\"\n",             // whitespace
	} {
		if _, err := loadCfg(t, body); err == nil || !strings.Contains(err.Error(), "manifest_refs") {
			t.Errorf("%q should be refused naming manifest_refs, got %v", body, err)
		}
	}
}
