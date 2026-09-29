package privatenotes

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"filippo.io/age"
)

func TestStoreLifecycleEncryptsAndSearchesMetadataOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-notes")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	initResult, err := store.Maintain(context.Background(), "init-encryption")
	if err != nil {
		t.Fatal(err)
	}
	if initResult.Recipient == "" || !initResult.IdentityCreated {
		t.Fatalf("unexpected init result: %#v", initResult)
	}

	bodyMarker := "BODY_ONLY_9f7c2c"
	write, err := store.Write(WriteRequest{
		Title: "生产恢复入口", Category: "recovery", Summary: "记录生产恢复资料的位置",
		Tags: []string{"recovery", "nexus"}, Content: "# 私密正文\n\n" + bodyMarker + "\nTOKEN=secret-value", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if write.Path != "notes/recovery/生产恢复入口.md" || !strings.HasSuffix(write.EncryptedPath, ".md.age") {
		t.Fatalf("unexpected write paths: %#v", write)
	}
	plain, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(write.Path)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(write.EncryptedPath)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), bodyMarker) || strings.Contains(string(encrypted), "secret-value") {
		t.Fatal("encrypted backup contains plaintext")
	}
	decrypted := decryptForTest(t, root, encrypted)
	if string(decrypted) != string(plain) {
		t.Fatal("decrypted backup does not match plaintext")
	}

	matches, err := store.Search(context.Background(), "生产恢复", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Title != "生产恢复入口" || matches[0].Summary == "" {
		t.Fatalf("unexpected metadata search results: %#v", matches)
	}
	bodyMatches, err := store.Search(context.Background(), bodyMarker, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodyMatches) != 0 {
		t.Fatalf("private note body must not be searchable: %#v", bodyMatches)
	}

	read, err := store.Read(write.Path, 256000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Content, bodyMarker) || !read.ContainsSecret {
		t.Fatalf("explicit read did not return plaintext: %#v", read)
	}
	status, err := store.Status(context.Background(), "check")
	if err != nil {
		t.Fatal(err)
	}
	if !status.EncryptedBackupOK || !status.PlaintextGitIgnored || !status.KeysGitIgnored || status.NotesCount != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}

	if _, err := store.Delete(write.Path, false); err != ErrConfirmationRequired {
		t.Fatalf("delete without confirmation error = %v", err)
	}
	deleted, err := store.Delete(write.Path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.DeletedPlaintext || !deleted.DeletedEncrypted {
		t.Fatalf("unexpected delete result: %#v", deleted)
	}
	for _, rel := range []string{write.Path, write.EncryptedPath} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("deleted path still exists: %s err=%v", rel, err)
		}
	}
}

func TestStoreOverwriteAndUTF8Truncation(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "private-notes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Maintain(context.Background(), "init"); err != nil {
		t.Fatal(err)
	}
	created, err := store.Write(WriteRequest{Path: "notes/profile/utf8.md", Content: "你a", Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(WriteRequest{Path: created.Path, Content: "replacement", Confirmed: true}); err != ErrNoteExists {
		t.Fatalf("write without overwrite error = %v", err)
	}
	if _, err := store.Write(WriteRequest{Path: created.Path, Content: "你a", Confirmed: true, Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		max  int
		want string
	}{
		{max: 1, want: ""},
		{max: 3, want: "你"},
		{max: 4, want: "你a"},
	} {
		content, truncated := truncateUTF8("你a", test.max)
		if content != test.want || !utf8.ValidString(content) || truncated != (test.max < 4) {
			t.Fatalf("max=%d content=%q truncated=%v want=%q", test.max, content, truncated, test.want)
		}
	}
}

func TestMetadataReaderStopsAtFrontmatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.md")
	bodyMarker := "BODY_MUST_NOT_ENTER_METADATA_8d4f"
	content := "---\ntitle: Safe title\nsummary: Safe summary\ntags: safe,metadata\ncontains_secret: true\n---\n\n" + strings.Repeat("x", maxMetadataBytes) + bodyMarker
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := readMetadataFrontmatter(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, "Safe summary") || strings.Contains(metadata, bodyMarker) || len(metadata) >= maxMetadataBytes {
		t.Fatalf("metadata reader crossed the frontmatter boundary: bytes=%d", len(metadata))
	}
}

func TestWriteFrontmatterHonorsRequestMetadataAndPreservesUnknownFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-notes")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Maintain(context.Background(), "init"); err != nil {
		t.Fatal(err)
	}
	incoming := `---
title: Old title
summary: LegacyOnlyMarker
tags: old,legacy
contains_secret: false
created_at: 2026-01-02T03:04:05Z
source: migration-test
---

TOKEN=private-body
`
	written, err := store.Write(WriteRequest{
		Path: "notes/recovery/frontmatter.md", Title: "New title", Summary: "New safe summary",
		Tags: []string{"new", "safe"}, Content: incoming, Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.Read(written.Path, MaxReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"title: 'New title'", "summary: 'New safe summary'", "tags: new,safe",
		"contains_secret: true", "created_at: 2026-01-02T03:04:05Z", "source: migration-test",
		"updated_at:", "TOKEN=private-body",
	} {
		if !strings.Contains(read.Content, expected) {
			t.Fatalf("rendered note missing %q:\n%s", expected, read.Content)
		}
	}
	for _, stale := range []string{"title: Old title", "summary: LegacyOnlyMarker", "tags: old,legacy"} {
		if strings.Contains(read.Content, stale) {
			t.Fatalf("rendered note retained stale controlled metadata %q:\n%s", stale, read.Content)
		}
	}
	matches, err := store.Search(context.Background(), "New safe summary", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Title != "New title" {
		t.Fatalf("updated metadata was not searchable: %#v", matches)
	}
	staleMatches, err := store.Search(context.Background(), "LegacyOnlyMarker", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(staleMatches) != 0 {
		t.Fatalf("stale metadata remained searchable: %#v", staleMatches)
	}
}

func TestStoreRejectsUnsafePathsAndSymlinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-notes")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Maintain(context.Background(), "init"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret.md", "/tmp/secret.md", "notes/.hidden/secret.md", "notes/recovery/secret.txt"} {
		if _, err := store.Write(WriteRequest{Path: path, Content: "secret", Confirmed: true}); err == nil {
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(root, plainDir, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.Write(WriteRequest{Path: "notes/linked/secret.md", Content: "secret", Confirmed: true}); ErrorCode(err) != "PRIVATE_NOTE_SYMLINK_REJECTED" {
		t.Fatalf("symlink path error = %v", err)
	}
}

func decryptForTest(t *testing.T, root string, encrypted []byte) []byte {
	t.Helper()
	identityBytes, err := os.ReadFile(filepath.Join(root, keyDir, identityFile))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(identityBytes)))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := age.Decrypt(strings.NewReader(string(encrypted)), identity)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestStatusDetectsAndRepairsStaleEncryptedBackup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-notes")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Maintain(context.Background(), "init"); err != nil {
		t.Fatal(err)
	}
	written, err := store.Write(WriteRequest{Title: "demo", Category: "audit", Content: "version one", Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(root, filepath.FromSlash(written.Path))
	if err := atomicWrite(plainPath, []byte("version two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(context.Background(), "check")
	if err != nil {
		t.Fatal(err)
	}
	if status.EncryptedBackupOK || len(status.StaleEncrypted) != 1 || status.StaleEncrypted[0] != written.EncryptedPath {
		t.Fatalf("stale backup was not reported: %#v", status)
	}
	if _, err := store.Maintain(context.Background(), "sync-encrypted"); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status(context.Background(), "check")
	if err != nil {
		t.Fatal(err)
	}
	if !status.EncryptedBackupOK {
		t.Fatalf("sync-encrypted did not repair backup: %#v", status)
	}
}

func TestStatusDetectsOrphanedEncryptedBackup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-notes")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Maintain(context.Background(), "init"); err != nil {
		t.Fatal(err)
	}
	written, err := store.Write(WriteRequest{Title: "demo", Category: "audit", Content: "version one", Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(written.Path))); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(context.Background(), "check")
	if err != nil {
		t.Fatal(err)
	}
	if status.EncryptedBackupOK || len(status.OrphanedEncrypted) != 1 || status.OrphanedEncrypted[0] != written.EncryptedPath {
		t.Fatalf("orphaned backup was not reported: %#v", status)
	}
}
