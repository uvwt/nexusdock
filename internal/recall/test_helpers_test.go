package recall

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, store *Store, path, content string) {
	t.Helper()
	abs := filepath.Join(store.Root(), filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
