package skillcatalog

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const document = "---\nname: sample\nversion: 1.0.0\ndescription: Sample Skill\n---\n\nRead a text file.\n"

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for n, v := range files {
		w, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(v)); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestImmutableVersionsSurviveRestart(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	data := archive(t, map[string]string{"sample/SKILL.md": document, "sample/references/guide.md": "hello"})
	e, err := s.Put(data, Metadata{Platforms: []string{"linux"}})
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.Put(data, Metadata{})
	if err != nil || same.SHA256 != e.SHA256 || len(same.Metadata.Platforms) != 1 {
		t.Fatalf("idempotent upload: %+v %v", same, err)
	}
	_, err = s.Put(archive(t, map[string]string{"SKILL.md": document + "changed"}), Metadata{})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting version: %v", err)
	}
	s = New(root)
	if content, err := s.ReadFile("sample", "1.0.0", "references/guide.md"); err != nil || content != "hello" {
		t.Fatalf("reference readback %q %v", content, err)
	}
	if _, err := s.ReadFile("sample", "1.0.0", "../../etc/passwd"); err == nil {
		t.Fatal("preview escaped archive")
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list after restart: %v %v", list, err)
	}
	_, got, err := s.Archive("sample", "1.0.0")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("download mismatch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample", "1.0.0", "package"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Archive("sample", "1.0.0"); err == nil {
		t.Fatal("tampered archive accepted")
	}
}
func TestRejectUnsafePackages(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/windows", "a/../../b", "a\\b", ".env", ".env.production", "id_ed25519", "x/.git/config"} {
		t.Run(name, func(t *testing.T) {
			_, err := Inspect(archive(t, map[string]string{"SKILL.md": document, name: "x"}))
			if err == nil {
				t.Fatal("unsafe member accepted")
			}
		})
	}
	for _, doc := range []string{"missing frontmatter", "---\nname: ../bad\nversion: 1.0.0\ndescription: bad\n---\nx", "---\nname: a\nversion: nope\ndescription: bad\n---\nx", "---\nname: a\nname: b\nversion: 1.0.0\ndescription: bad\n---\nx"} {
		if _, err := Inspect(archive(t, map[string]string{"SKILL.md": doc})); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
	if _, err := Inspect(archive(t, map[string]string{"SKILL.md": document, "key": "-----BEGIN OPENSSH PRIVATE KEY-----"})); err == nil {
		t.Fatal("private key accepted")
	}
}
func TestTarPackageAndLinks(t *testing.T) {
	for _, kind := range []byte{tar.TypeReg, tar.TypeSymlink, tar.TypeLink} {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "SKILL.md", Size: int64(len(document)), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(document))
		h := &tar.Header{Name: "reference", Typeflag: kind, Mode: 0600}
		if kind != tar.TypeReg {
			h.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Close()
		gz.Close()
		_, err := Inspect(b.Bytes())
		if kind == tar.TypeReg && err != nil {
			t.Fatal(err)
		}
		if kind != tar.TypeReg && err == nil {
			t.Fatal("link accepted")
		}
	}
}
func TestConcurrentUploadPublishesOneVersion(t *testing.T) {
	s := New(t.TempDir())
	data := archive(t, map[string]string{"SKILL.md": document})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.Put(data, Metadata{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	items, err := s.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %v", items, err)
	}
}
func TestArchiveResourceLimits(t *testing.T) {
	if _, err := Inspect(make([]byte, MaxArchive+1)); err == nil {
		t.Fatal("oversized upload accepted")
	}
	data := archive(t, map[string]string{"SKILL.md": document, "large": string(make([]byte, MaxExpanded+1))})
	if _, err := Inspect(data); err == nil {
		t.Fatal("zip bomb accepted")
	}
}
