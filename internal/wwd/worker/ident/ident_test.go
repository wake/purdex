package ident

import "testing"

func TestKeyAndParseKeyRoundTrip(t *testing.T) {
	for _, r := range []Ref{
		{"air26", "j_01JCABC"},
		{"mlab", "j_x"},
		{"a/b", "j_x"},     // an alias may hold a slash: the id never does, so the key splits at the last one
		{"my host", "j_x"}, // spaces
		{"墨水-1", "j_x"},    // not ASCII
		{"a.b:c-d", "j_y"},
	} {
		key := r.Key()
		if want := r.Source + "/" + r.ID; key != want {
			t.Errorf("Key(%+v) = %q, want %q", r, key, want)
		}
		got, ok := ParseKey(key)
		if !ok || got != r {
			t.Errorf("ParseKey(%q) = %+v %v, want %+v", key, got, ok, r)
		}
	}
}

func TestParseKeyRefusesWhatIsNotAKey(t *testing.T) {
	for _, k := range []string{"", "j_x", "/j_x", "air26/", "/", "air26//", "air26/j/x"} {
		// "air26/j/x" is {"air26/j", "x"}: a key whose id is the last part; only the shapes with an empty side are refused
		got, ok := ParseKey(k)
		if k == "air26/j/x" {
			if !ok || got.Source != "air26/j" || got.ID != "x" {
				t.Errorf("ParseKey(%q) = %+v %v", k, got, ok)
			}
			continue
		}
		if ok {
			t.Errorf("ParseKey(%q) = %+v, want refused", k, got)
		}
	}
}

func TestSameIDFromTwoSourcesIsTwoKeys(t *testing.T) {
	a, b := Ref{"air26", "j_same"}, Ref{"mserv", "j_same"}
	if a.Key() == b.Key() || a == b {
		t.Fatalf("the same id from two connections must be two identities: %q %q", a.Key(), b.Key())
	}
}

func TestOriginTag(t *testing.T) {
	// sha256 of the source, first 16 hex characters
	for src, want := range map[string]string{
		"air26": "ea225edf2a219ec6",
		"":      "e3b0c44298fc1c14",
		"a/b c": "0af99a6091695385",
	} {
		if got := OriginTag(src); got != want {
			t.Errorf("OriginTag(%q) = %q, want %q", src, got, want)
		}
	}
	if OriginTag("air26") == OriginTag("air27") {
		t.Error("two sources, one tag")
	}
}

func TestRefTag(t *testing.T) {
	r := Ref{"air26", "j_x"}
	if r.Tag() != OriginTag("air26") {
		t.Errorf("Tag = %q", r.Tag())
	}
}
