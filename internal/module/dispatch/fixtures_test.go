package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// m0Fixture is the self-describing golden-fixture envelope (docs/fixtures/m0).
type m0Fixture struct {
	Name       string          `json:"name"`
	Endpoint   string          `json:"endpoint"`
	Kind       string          `json:"kind"`
	HTTPStatus int             `json:"http_status"`
	Payload    json.RawMessage `json:"payload"`
}

// fixtureDir is the golden-fixture directory relative to this package.
func fixtureDir() string {
	return filepath.Join("..", "..", "..", "docs", "fixtures", "m0")
}

// loadFixture reads and parses one golden fixture by file name.
func loadFixture(t *testing.T, file string) m0Fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir(), file))
	if err != nil {
		t.Fatalf("read fixture %s: %v", file, err)
	}
	var f m0Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", file, err)
	}
	if len(f.Payload) == 0 {
		t.Fatalf("fixture %s has no payload", file)
	}
	return f
}
