package nexusapp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeHTTPReturnsListenFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	server := &http.Server{Addr: occupied.Addr().String(), Handler: http.NotFoundHandler()}
	err = serveHTTP(context.Background(), server)
	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("listen failure=%v", err)
	}
}

func TestServeHTTPShutsDownOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not stop after context cancellation")
	}
}

func TestMainReturnsFailureForInvalidAdminCommand(t *testing.T) {
	if code := Main([]string{"nexusdock", "admin", "unknown"}); code != 1 {
		t.Fatalf("exit code=%d want=1", code)
	}
}

func TestWarnLegacyPrivateNotesIfPresentSilentWhenAbsentOrEmpty(t *testing.T) {
	repoDir := t.TempDir()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// 目录不存在时不告警。
	warnLegacyPrivateNotesIfPresent(repoDir, logger)
	if buf.Len() != 0 {
		t.Fatalf("expected no log when private-notes is absent, got: %s", buf.String())
	}

	// 仅存在空目录时不告警。
	if err := os.MkdirAll(filepath.Join(repoDir, "private-notes", "notes", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "private-notes", "encrypted", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	warnLegacyPrivateNotesIfPresent(repoDir, logger)
	if buf.Len() != 0 {
		t.Fatalf("expected no log when private-notes directories are empty, got: %s", buf.String())
	}
}

func TestWarnLegacyPrivateNotesIfPresentWarnsAndLeavesFilesUntouched(t *testing.T) {
	tests := []struct {
		name          string
		relPath       string
		content       string
		wantNotes     bool
		wantEncrypted bool
	}{
		{
			name:          "plaintext notes",
			relPath:       filepath.Join("private-notes", "notes", "recovery", "secret.md"),
			content:       "legacy plaintext secret\n",
			wantNotes:     true,
			wantEncrypted: false,
		},
		{
			name:          "encrypted backups",
			relPath:       filepath.Join("private-notes", "encrypted", "recovery", "secret.md.age"),
			content:       "age-encrypted-bytes",
			wantNotes:     false,
			wantEncrypted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := t.TempDir()
			target := filepath.Join(repoDir, tc.relPath)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			warnLegacyPrivateNotesIfPresent(repoDir, logger)

			var record map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
				t.Fatalf("parse warn log %q: %v", buf.String(), err)
			}
			if record["level"] != "WARN" {
				t.Fatalf("level = %v, want WARN", record["level"])
			}
			msg, _ := record["msg"].(string)
			for _, snippet := range []string{"private_note_manage", "isolated from ordinary Recall", "age CLI"} {
				if !strings.Contains(msg, snippet) {
					t.Fatalf("warn msg %q missing %q", msg, snippet)
				}
			}
			wantDir := filepath.Join(repoDir, "private-notes")
			if record["path"] != wantDir {
				t.Fatalf("path = %v, want %q", record["path"], wantDir)
			}
			if record["has_notes"] != tc.wantNotes || record["has_encrypted"] != tc.wantEncrypted {
				t.Fatalf("flags = (notes:%v, encrypted:%v), want (%v, %v)", record["has_notes"], record["has_encrypted"], tc.wantNotes, tc.wantEncrypted)
			}

			// 确认探测只读：存量文件原封未动。
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("legacy file must remain untouched: %v", err)
			}
			if string(got) != tc.content {
				t.Fatalf("legacy file content mutated: got %q want %q", string(got), tc.content)
			}
		})
	}
}
