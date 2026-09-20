// Package skillcatalog stores immutable, reviewed-for-structure Skill archives.
// Uploading never executes a package or grants it access to a node.
package skillcatalog

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MaxArchive = 32 << 20
const MaxExpanded = 64 << 20
const MaxFiles = 2000

var ErrConflict = errors.New("same name and version already contain a different package")
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type Metadata struct {
	ReviewNote   string   `json:"review_note"`
	Portability  string   `json:"portability"`
	Usage        Usage    `json:"usage"`
	Source       string   `json:"source"`
	Platforms    []string `json:"platforms"`
	Dependencies string   `json:"dependencies"`
}

type Entry struct {
	Constraints   []string `json:"constraints"`
	InstallSHA256 string   `json:"install_sha256"`
	installZIP    []byte
	Name          string    `json:"name" yaml:"name"`
	Version       string    `json:"version" yaml:"version"`
	Description   string    `json:"description" yaml:"description"`
	SHA256        string    `json:"sha256"`
	Size          int       `json:"size_bytes"`
	Files         []string  `json:"files"`
	CreatedAt     time.Time `json:"created_at"`
	Metadata      Metadata  `json:"metadata"`
}

type Store struct {
	root string
	mu   sync.Mutex
}

func New(root string) *Store { return &Store{root: root} }

func validID(name, version string) bool {
	return namePattern.MatchString(name) && versionPattern.MatchString(version)
}

// 包只在内存中解包，不把不受信任的归档路径写到宿主文件系统。
func Inspect(data []byte) (Entry, error) {
	var entry Entry
	if len(data) == 0 || len(data) > MaxArchive {
		return entry, errors.New("archive must be between 1 byte and 32 MiB")
	}
	files := map[string][]byte{}
	modes := map[string]os.FileMode{}
	total := 0
	add := func(name string, size int64, mode os.FileMode, r io.Reader) error {
		if strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("unsafe archive path %q", name)
		}
		name = strings.TrimPrefix(name, "./")
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return fmt.Errorf("unsafe archive path %q", name)
			}
		}
		name = path.Clean(name)
		if mode.IsDir() {
			return nil
		}
		if !mode.IsRegular() || name == "." {
			return fmt.Errorf("unsupported archive member %q", name)
		}
		if _, found := files[name]; found {
			return fmt.Errorf("duplicate archive member %q", name)
		}
		if len(files) >= MaxFiles || size < 0 || size > int64(MaxExpanded-total) {
			return errors.New("expanded package exceeds 64 MiB or 2000 files")
		}
		b, err := io.ReadAll(io.LimitReader(r, int64(MaxExpanded-total)+1))
		if err != nil {
			return err
		}
		total += len(b)
		if total > MaxExpanded {
			return errors.New("expanded package exceeds 64 MiB")
		}
		base := strings.ToLower(path.Base(name))
		if base == ".env" || strings.HasPrefix(base, ".env.") || base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".pem") || strings.Contains("/"+name+"/", "/.git/") {
			return fmt.Errorf("remove credential or repository state file %q", name)
		}
		if bytes.Contains(b, []byte("PRIVATE KEY-----")) {
			return fmt.Errorf("private key material in %q", name)
		}
		files[name] = b
		modes[name] = mode.Perm() & 0755
		return nil
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return entry, err
		}
		if len(z.File) > MaxFiles {
			return entry, errors.New("too many archive members")
		}
		for _, f := range z.File {
			r, err := f.Open()
			if err != nil {
				return entry, err
			}
			err = add(f.Name, int64(f.UncompressedSize64), f.Mode(), r)
			r.Close()
			if err != nil {
				return entry, err
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return entry, errors.New("upload a ZIP or tar.gz package")
		}
		defer gz.Close()
		// Limit the entire decompressed stream as well as individual files (including headers).
		tr := tar.NewReader(io.LimitReader(gz, MaxExpanded+MaxFiles*1024+1))
		count := 0
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return entry, err
			}
			count++
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
				return entry, fmt.Errorf("unsupported archive member %q", h.Name)
			}
			if count > MaxFiles {
				return entry, errors.New("too many archive members")
			}
			if err := add(h.Name, h.Size, h.FileInfo().Mode(), tr); err != nil {
				return entry, err
			}
		}
	}
	root := ""
	if _, ok := files["SKILL.md"]; !ok {
		for name := range files {
			if path.Base(name) == "SKILL.md" && strings.Count(name, "/") == 1 {
				if root != "" {
					return entry, errors.New("archive must contain one Skill")
				}
				root = strings.TrimSuffix(name, "SKILL.md")
			}
		}
		if root == "" {
			return entry, errors.New("missing root SKILL.md")
		}
	}
	doc := files[root+"SKILL.md"]
	if !utf8.Valid(doc) || len(doc) > 1<<20 {
		return entry, errors.New("SKILL.md must be UTF-8 and at most 1 MiB")
	}
	text := strings.ReplaceAll(string(doc), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return entry, errors.New("SKILL.md requires YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return entry, errors.New("unclosed YAML frontmatter")
	}
	var front struct {
		Name        string `yaml:"name"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &front); err != nil {
		return entry, fmt.Errorf("invalid frontmatter: %w", err)
	}
	entry.Name, entry.Version, entry.Description = front.Name, front.Version, front.Description
	if !validID(entry.Name, entry.Version) || strings.TrimSpace(entry.Description) == "" || strings.TrimSpace(text[4+end+5:]) == "" {
		return entry, errors.New("SKILL.md requires name, semantic version, description and body")
	}
	for name := range files {
		if root != "" && !strings.HasPrefix(name, root) {
			return entry, errors.New("files outside Skill directory")
		}
		entry.Files = append(entry.Files, strings.TrimPrefix(name, root))
	}
	sort.Strings(entry.Files)
	entry.Constraints = portabilityConstraints(files)
	// AgentDock 接受 ZIP。统一根路径和文件次序，保留原件供下载，同时生成可重复的安装包。
	var packed bytes.Buffer
	zw := zip.NewWriter(&packed)
	for _, name := range entry.Files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(modes[root+name])
		w, err := zw.CreateHeader(h)
		if err != nil {
			return entry, err
		}
		if _, err := w.Write(files[root+name]); err != nil {
			return entry, err
		}
	}
	if err := zw.Close(); err != nil {
		return entry, err
	}
	if packed.Len() > MaxArchive {
		return entry, errors.New("normalized installation ZIP exceeds 32 MiB")
	}
	entry.installZIP = packed.Bytes()
	installHash := sha256.Sum256(entry.installZIP)
	entry.InstallSHA256 = hex.EncodeToString(installHash[:])
	h := sha256.Sum256(data)
	entry.SHA256 = hex.EncodeToString(h[:])
	entry.Size = len(data)
	entry.CreatedAt = time.Now().UTC()
	return entry, nil
}

func (s *Store) Put(data []byte, metadata Metadata) (Entry, error) {
	e, err := Inspect(data)
	if err != nil {
		return e, err
	}
	if len(metadata.Source) > 2048 || len(metadata.Dependencies) > 8192 {
		return e, errors.New("metadata is too long")
	}
	for _, p := range metadata.Platforms {
		if p != "darwin" && p != "linux" && p != "windows" {
			return e, errors.New("invalid platform")
		}
	}
	e.Metadata = metadata
	if err := validatePortability(&e); err != nil {
		return e, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, err := s.Get(e.Name, e.Version); err == nil {
		if old.SHA256 == e.SHA256 {
			return old, nil
		}
		return e, ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return e, err
	}
	parent := filepath.Join(s.root, e.Name)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return e, err
	}
	dir, err := os.MkdirTemp(parent, ".upload-")
	if err != nil {
		return e, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "package"), data, 0600); err != nil {
		return e, err
	}
	if err := os.WriteFile(filepath.Join(dir, "install.zip"), e.installZIP, 0600); err != nil {
		return e, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), b, 0600); err != nil {
		return e, err
	}
	// 完整目录原子发布；进程崩溃时读者不会看到半个版本。
	if err := os.Rename(dir, filepath.Join(parent, e.Version)); err != nil {
		return e, err
	}
	return e, nil
}

func (s *Store) Get(name, version string) (Entry, error) {
	var e Entry
	if !validID(name, version) {
		return e, errors.New("invalid Skill name or version")
	}
	b, err := os.ReadFile(filepath.Join(s.root, name, version, "metadata.json"))
	if err != nil {
		return e, err
	}
	err = json.Unmarshal(b, &e)
	return e, err
}

// 用法与审查说明允许更新，包内容和两个摘要始终保持不可变。
func (s *Store) Review(name, version string, metadata Metadata) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.Get(name, version)
	if err != nil {
		return e, err
	}
	if len(metadata.Source) > 2048 || len(metadata.Dependencies) > 8192 {
		return e, errors.New("metadata is too long")
	}
	for _, p := range metadata.Platforms {
		if p != "darwin" && p != "linux" && p != "windows" {
			return e, errors.New("invalid platform")
		}
	}
	e.Metadata = metadata
	if err := validatePortability(&e); err != nil {
		return e, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	f, err := os.CreateTemp(filepath.Join(s.root, name, version), ".review-")
	if err != nil {
		return e, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return e, err
	}
	if err := f.Close(); err != nil {
		return e, err
	}
	if err := os.Rename(f.Name(), filepath.Join(s.root, name, version, "metadata.json")); err != nil {
		return e, err
	}
	return e, nil
}
func (s *Store) Archive(name, version string) (Entry, []byte, error) {
	e, err := s.Get(name, version)
	if err != nil {
		return e, nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.root, name, version, "package"))
	if err != nil {
		return e, nil, err
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != e.SHA256 {
		return e, nil, errors.New("stored package checksum mismatch")
	}
	return e, b, nil
}

func (s *Store) InstallArchive(name, version string) (Entry, []byte, error) {
	e, err := s.Get(name, version)
	if err != nil {
		return e, nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.root, name, version, "install.zip"))
	if err != nil {
		return e, nil, err
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != e.InstallSHA256 {
		return e, nil, errors.New("stored installation ZIP checksum mismatch")
	}
	return e, b, nil
}

func (s *Store) ReadFile(name, version, file string) (string, error) {
	_, b, err := s.InstallArchive(name, version)
	if err != nil {
		return "", err
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", err
	}
	for _, f := range z.File {
		if f.Name != file {
			continue
		}
		if f.UncompressedSize64 > 1<<20 {
			return "", errors.New("file preview is limited to 1 MiB")
		}
		r, err := f.Open()
		if err != nil {
			return "", err
		}
		defer r.Close()
		content, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
		if err != nil {
			return "", err
		}
		if !utf8.Valid(content) || bytes.ContainsRune(content, 0) {
			return "", errors.New("binary file; download the package to inspect it")
		}
		return string(content), nil
	}
	return "", os.ErrNotExist
}
func (s *Store) List() ([]Entry, error) {
	out := []Entry{}
	names, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		if !n.IsDir() || !namePattern.MatchString(n.Name()) {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(s.root, n.Name()))
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			if !v.IsDir() || strings.HasPrefix(v.Name(), ".") {
				continue
			}
			e, err := s.Get(n.Name(), v.Name())
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
