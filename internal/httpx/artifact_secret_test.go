package httpx

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/uvwt/nexusdock/internal/config"
)

func TestArtifactSigningSecretIsAtomicAndSecure(t *testing.T) {
	dataDir := t.TempDir()
	const workers = 8
	results := make(chan []byte, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server := &Server{cfg: config.Config{NexusDataDir: dataDir}}
			secret, err := server.artifactSigningSecret()
			if err != nil {
				errs <- err
				return
			}
			results <- secret
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	var expected []byte
	for secret := range results {
		if len(secret) != 32 {
			t.Fatalf("secret bytes = %d", len(secret))
		}
		if expected == nil {
			expected = append([]byte(nil), secret...)
			continue
		}
		if !bytes.Equal(secret, expected) {
			t.Fatal("concurrent servers observed different Artifact signing secrets")
		}
	}

	secretDir := filepath.Join(dataDir, "secrets")
	secretPath := filepath.Join(secretDir, "artifact-url-secret")
	dirInfo, err := os.Stat(secretDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("secret dir mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("secret file mode = %o, want 600", got)
	}
	raw, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw, []byte("\n")) {
		t.Fatalf("secret file must end with newline: %q", raw)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || !bytes.Equal(decoded, expected) {
		t.Fatalf("stored secret is not canonical hex: err=%v", err)
	}
}

func TestArtifactSigningSecretRejectsSymlink(t *testing.T) {
	dataDir := t.TempDir()
	secretDir := filepath.Join(dataDir, "secrets")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dataDir, "target")
	if err := os.WriteFile(target, []byte(strings.Repeat("a", 64)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(secretDir, "artifact-url-secret")
	if err := os.Symlink(target, secretPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := &Server{cfg: config.Config{NexusDataDir: dataDir}}
	if _, err := server.artifactSigningSecret(); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink secret error = %v", err)
	}
}
