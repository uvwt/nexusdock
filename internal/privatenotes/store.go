package privatenotes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"filippo.io/age"
)

const (
	plainDir      = "notes"
	encryptedDir  = "encrypted"
	keyDir        = ".keys"
	identityFile  = "private-notes-age-identity.txt"
	recipientFile = "recipients.txt"

	MaxSearchResults = 100
	MaxReadBytes     = 1 << 20
	maxMetadataBytes = 64 << 10
)

var (
	ErrConfirmationRequired = coded("CONFIRMATION_REQUIRED", "private note mutation requires confirmed=true")
	ErrNoteExists           = coded("PRIVATE_NOTE_EXISTS", "private note already exists; pass overwrite=true")
	ErrNoteNotFound         = coded("PRIVATE_NOTE_NOT_FOUND", "private note does not exist")
	ErrEncryptedMissing     = coded("PRIVATE_NOTE_ENCRYPTED_MISSING", "encrypted private note backup is missing")
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func coded(code, message string) error { return &Error{Code: code, Message: message} }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return "PRIVATE_NOTE_OPERATION_FAILED"
}

type Store struct {
	root string
	mu   sync.RWMutex
}

type Summary struct {
	Path           string   `json:"path"`
	EncryptedPath  string   `json:"encrypted_path"`
	Category       string   `json:"category,omitempty"`
	Title          string   `json:"title,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	UpdatedAt      string   `json:"updated_at,omitempty"`
	ContainsSecret bool     `json:"contains_secret"`
	Score          int      `json:"score,omitempty"`
}

type WriteRequest struct {
	Path      string   `json:"path"`
	Category  string   `json:"category"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Tags      []string `json:"tags"`
	Content   string   `json:"content"`
	Confirmed bool     `json:"confirmed"`
	Overwrite bool     `json:"overwrite"`
}

type WriteResult struct {
	Action        string `json:"action"`
	Root          string `json:"root"`
	Path          string `json:"path"`
	EncryptedPath string `json:"encrypted_path"`
	Written       bool   `json:"written"`
	Encrypted     bool   `json:"encrypted"`
	Algorithm     string `json:"algorithm"`
}

type ReadResult struct {
	Action         string `json:"action"`
	Root           string `json:"root"`
	Path           string `json:"path"`
	EncryptedPath  string `json:"encrypted_path"`
	Content        string `json:"content"`
	Truncated      bool   `json:"truncated"`
	ContainsSecret bool   `json:"contains_secret"`
}

type DeleteResult struct {
	Action           string `json:"action"`
	Root             string `json:"root"`
	Path             string `json:"path"`
	EncryptedPath    string `json:"encrypted_path"`
	DeletedPlaintext bool   `json:"deleted_plaintext"`
	DeletedEncrypted bool   `json:"deleted_encrypted"`
}

type StatusResult struct {
	Action              string    `json:"action"`
	Root                string    `json:"root"`
	Notes               []Summary `json:"notes,omitempty"`
	Count               int       `json:"count,omitempty"`
	NotesCount          int       `json:"notes_count,omitempty"`
	MissingEncrypted    []string  `json:"missing_encrypted,omitempty"`
	StaleEncrypted      []string  `json:"stale_encrypted,omitempty"`
	OrphanedEncrypted   []string  `json:"orphaned_encrypted,omitempty"`
	EncryptedBackupOK   bool      `json:"encrypted_backup_ok"`
	PlaintextGitIgnored bool      `json:"plaintext_git_ignored"`
	KeysGitIgnored      bool      `json:"keys_git_ignored"`
}

type MaintainResult struct {
	Action          string `json:"action"`
	Root            string `json:"root"`
	Recipient       string `json:"recipient,omitempty"`
	IdentityCreated bool   `json:"identity_created,omitempty"`
	EncryptedCount  int    `json:"encrypted_count,omitempty"`
	Algorithm       string `json:"algorithm"`
}

func New(root string) (*Store, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return nil, coded("PRIVATE_NOTES_ROOT_REQUIRED", "private notes root is required")
	}
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		root = abs
	}
	store := &Store{root: root}
	if err := store.initTree(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) Search(ctx context.Context, query string, maxResults int) ([]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, coded("MISSING_QUERY", "query is required")
	}
	if maxResults <= 0 || maxResults > MaxSearchResults {
		maxResults = 8
	}
	terms := queryTerms(query)
	if len(terms) == 0 {
		return nil, coded("INVALID_QUERY", "query has no searchable terms")
	}

	items, err := s.listLocked(ctx)
	if err != nil {
		return nil, err
	}
	queryLower := strings.ToLower(query)
	matches := make([]Summary, 0, len(items))
	for _, item := range items {
		searchable := strings.ToLower(strings.Join([]string{item.Path, item.Category, item.Title, item.Summary, strings.Join(item.Tags, " ")}, "\n"))
		score := 0
		for _, term := range terms {
			if strings.Contains(strings.ToLower(item.Title), term) {
				score += 8
			}
			if strings.Contains(strings.ToLower(item.Summary), term) {
				score += 5
			}
			if strings.Contains(strings.ToLower(strings.Join(item.Tags, " ")), term) {
				score += 4
			}
			if strings.Contains(strings.ToLower(item.Path), term) {
				score += 3
			}
			if strings.Contains(strings.ToLower(item.Category), term) {
				score += 2
			}
		}
		if strings.Contains(searchable, queryLower) {
			score += 12
		}
		if score == 0 {
			continue
		}
		item.Score = score
		matches = append(matches, item)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Path < matches[j].Path
	})
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	return matches, nil
}

func (s *Store) Read(path string, maxBytes int) (ReadResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rel, abs, err := s.resolveNote(path, "", "")
	if err != nil {
		return ReadResult{}, err
	}
	content, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return ReadResult{}, ErrNoteNotFound
	}
	if err != nil {
		return ReadResult{}, fmt.Errorf("read private note: %w", err)
	}
	if maxBytes <= 0 || maxBytes > MaxReadBytes {
		maxBytes = 256000
	}
	body, truncated := truncateUTF8(string(content), maxBytes)
	metadata := summaryFromContent(rel, string(content), fileModTime(abs))
	return ReadResult{
		Action: "read", Root: s.root, Path: rel, EncryptedPath: encryptedPath(rel),
		Content: body, Truncated: truncated, ContainsSecret: metadata.ContainsSecret,
	}, nil
}

func (s *Store) Write(req WriteRequest) (WriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !req.Confirmed {
		return WriteResult{}, ErrConfirmationRequired
	}
	if strings.TrimSpace(req.Content) == "" {
		return WriteResult{}, coded("MISSING_CONTENT", "content is required")
	}
	if err := s.initTree(); err != nil {
		return WriteResult{}, err
	}
	rel, abs, err := s.resolveNote(req.Path, req.Category, req.Title)
	if err != nil {
		return WriteResult{}, err
	}
	previous, readErr := os.ReadFile(abs)
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return WriteResult{}, fmt.Errorf("read existing private note: %w", readErr)
	}
	if existed && !req.Overwrite {
		return WriteResult{}, ErrNoteExists
	}

	content := renderContent(rel, req, string(previous), existed)
	recipients, err := s.recipients()
	if err != nil {
		return WriteResult{}, err
	}
	encrypted, err := encrypt([]byte(content), recipients)
	if err != nil {
		return WriteResult{}, fmt.Errorf("encrypt private note: %w", err)
	}
	encRel := encryptedPath(rel)
	encAbs, err := s.resolveInternal(encRel)
	if err != nil {
		return WriteResult{}, err
	}
	previousEncrypted, encryptedReadErr := os.ReadFile(encAbs)
	encryptedExisted := encryptedReadErr == nil
	if encryptedReadErr != nil && !errors.Is(encryptedReadErr, fs.ErrNotExist) {
		return WriteResult{}, fmt.Errorf("read existing encrypted private note: %w", encryptedReadErr)
	}

	// 密文是明文的派生副本。先发布密文、最后发布明文，保证进程若在两步之间退出，
	// 明文仍是唯一事实来源；状态检查/维护可以据此确定性重建密文，而不会把半提交
	// 的新明文误认为已经有对应备份。
	if err := atomicWrite(encAbs, encrypted, 0o600); err != nil {
		return WriteResult{}, fmt.Errorf("write encrypted private note: %w", err)
	}
	if err := atomicWrite(abs, []byte(content), 0o600); err != nil {
		rollbackErr := restoreFile(encAbs, previousEncrypted, encryptedExisted, 0o600)
		return WriteResult{}, errors.Join(fmt.Errorf("write private note: %w", err), rollbackErr)
	}
	return WriteResult{
		Action: "write", Root: s.root, Path: rel, EncryptedPath: encRel,
		Written: true, Encrypted: true, Algorithm: "age/X25519",
	}, nil
}

func (s *Store) Delete(path string, confirmed bool) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !confirmed {
		return DeleteResult{}, ErrConfirmationRequired
	}
	rel, abs, err := s.resolveNote(path, "", "")
	if err != nil {
		return DeleteResult{}, err
	}
	_, err = os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return DeleteResult{}, ErrNoteNotFound
	}
	if err != nil {
		return DeleteResult{}, fmt.Errorf("read private note before delete: %w", err)
	}
	encRel := encryptedPath(rel)
	encAbs, err := s.resolveInternal(encRel)
	if err != nil {
		return DeleteResult{}, err
	}
	encrypted, err := os.ReadFile(encAbs)
	if errors.Is(err, fs.ErrNotExist) {
		return DeleteResult{}, ErrEncryptedMissing
	}
	if err != nil {
		return DeleteResult{}, fmt.Errorf("read encrypted private note before delete: %w", err)
	}
	// 删除同样让明文最后提交：若进程在两步之间退出，只会留下“明文存在、密文缺失”
	// 的可修复状态，不会留下没有明文事实来源的孤儿密文。
	if err := removeFile(encAbs); err != nil {
		return DeleteResult{}, fmt.Errorf("delete encrypted private note: %w", err)
	}
	if err := removeFile(abs); err != nil {
		return DeleteResult{}, errors.Join(fmt.Errorf("delete private note: %w", err), restoreFile(encAbs, encrypted, true, 0o600))
	}
	removeEmptyParents(filepath.Dir(abs), filepath.Join(s.root, plainDir))
	removeEmptyParents(filepath.Dir(encAbs), filepath.Join(s.root, encryptedDir))
	return DeleteResult{
		Action: "delete", Root: s.root, Path: rel, EncryptedPath: encRel,
		DeletedPlaintext: true, DeletedEncrypted: true,
	}, nil
}

func (s *Store) Status(ctx context.Context, action string) (StatusResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "check"
	}
	items, err := s.listLocked(ctx)
	if err != nil {
		return StatusResult{}, err
	}
	result := StatusResult{
		Action: action, Root: s.root, NotesCount: len(items), EncryptedBackupOK: true,
		PlaintextGitIgnored: s.gitignoreContains("notes/"), KeysGitIgnored: s.gitignoreContains(".keys/"),
	}
	identity, identityErr := s.readIdentity()
	seenEncrypted := make(map[string]struct{}, len(items))
	for _, item := range items {
		seenEncrypted[item.EncryptedPath] = struct{}{}
		encAbs, resolveErr := s.resolveInternal(item.EncryptedPath)
		if resolveErr != nil {
			return StatusResult{}, resolveErr
		}
		if _, statErr := os.Stat(encAbs); errors.Is(statErr, fs.ErrNotExist) {
			result.MissingEncrypted = append(result.MissingEncrypted, item.EncryptedPath)
			continue
		} else if statErr != nil {
			return StatusResult{}, fmt.Errorf("stat encrypted private note: %w", statErr)
		}
		_, plainAbs, resolveErr := s.resolveNote(item.Path, "", "")
		if resolveErr != nil {
			return StatusResult{}, resolveErr
		}
		if identityErr != nil {
			result.StaleEncrypted = append(result.StaleEncrypted, item.EncryptedPath)
			continue
		}
		matches, compareErr := encryptedMatchesPlaintext(encAbs, plainAbs, identity)
		if compareErr != nil || !matches {
			result.StaleEncrypted = append(result.StaleEncrypted, item.EncryptedPath)
		}
	}
	encryptedRoot := filepath.Join(s.root, encryptedDir)
	if err := filepath.WalkDir(encryptedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == encryptedRoot {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".md.age") {
			return nil
		}
		if _, ok := seenEncrypted[rel]; !ok {
			result.OrphanedEncrypted = append(result.OrphanedEncrypted, rel)
		}
		return nil
	}); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return StatusResult{}, err
	}
	result.EncryptedBackupOK = len(result.MissingEncrypted) == 0 && len(result.StaleEncrypted) == 0 && len(result.OrphanedEncrypted) == 0
	switch action {
	case "check", "status":
		result.Action = "check"
	case "list":
		result.Notes = items
		result.Count = len(items)
	default:
		return StatusResult{}, coded("INVALID_PRIVATE_NOTE_STATUS_ACTION", "status action must be check or list")
	}
	return result, nil
}

func (s *Store) Maintain(ctx context.Context, action string) (MaintainResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "sync-encrypted"
	}
	if err := s.initTree(); err != nil {
		return MaintainResult{}, err
	}
	switch action {
	case "init", "init-encryption":
		identity, created, err := s.ensureIdentity()
		if err != nil {
			return MaintainResult{}, err
		}
		return MaintainResult{Action: "init", Root: s.root, Recipient: identity.Recipient().String(), IdentityCreated: created, Algorithm: "age/X25519"}, nil
	case "sync-encrypted", "encrypt-all":
		items, err := s.listLocked(ctx)
		if err != nil {
			return MaintainResult{}, err
		}
		recipients, err := s.recipients()
		if err != nil {
			return MaintainResult{}, err
		}
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return MaintainResult{}, err
			}
			_, abs, err := s.resolveNote(item.Path, "", "")
			if err != nil {
				return MaintainResult{}, err
			}
			plain, err := os.ReadFile(abs)
			if err != nil {
				return MaintainResult{}, err
			}
			sealed, err := encrypt(plain, recipients)
			if err != nil {
				return MaintainResult{}, err
			}
			encAbs, err := s.resolveInternal(item.EncryptedPath)
			if err != nil {
				return MaintainResult{}, err
			}
			if err := atomicWrite(encAbs, sealed, 0o600); err != nil {
				return MaintainResult{}, err
			}
		}
		return MaintainResult{Action: action, Root: s.root, EncryptedCount: len(items), Algorithm: "age/X25519"}, nil
	default:
		return MaintainResult{}, coded("INVALID_PRIVATE_NOTE_MAINTENANCE_ACTION", "maintenance action must be init, init-encryption, sync-encrypted, or encrypt-all")
	}
}

func (s *Store) initTree() error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return err
	}
	for _, rel := range []string{plainDir, encryptedDir, keyDir} {
		path := filepath.Join(s.root, rel)
		if err := ensureNoSymlink(s.root, rel); err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	files := map[string]string{
		"README.md":  "# private-notes\n\nNexusDock 管理私密笔记。`notes/` 保存本机明文，`encrypted/` 保存可提交 Git 的 age 密文，`.keys/` 保存本机 age identity。\n",
		"RULES.md":   "# private-notes 规则\n\n1. `notes/` 和 `.keys/` 永远不得提交 Git。\n2. 每次写入、覆盖和删除必须同时维护明文与 age 密文。\n3. 私密笔记搜索只读取标题、简介、标签、分类和路径，不读取正文。\n4. 私密笔记不进入普通 Recall 搜索、上下文包或 embedding。\n",
		".gitignore": "notes/\n.keys/\n*.tmp\n*.bak\n.DS_Store\n",
	}
	for rel, content := range files {
		path := filepath.Join(s.root, rel)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if err := atomicWrite(path, []byte(content), 0o600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return s.ensureGitignoreRules()
}

func (s *Store) ensureGitignoreRules() error {
	path := filepath.Join(s.root, ".gitignore")
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(content)
	changed := false
	for _, rule := range []string{"notes/", ".keys/"} {
		found := false
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == rule {
				found = true
				break
			}
		}
		if !found {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += rule + "\n"
			changed = true
		}
	}
	if changed {
		return atomicWrite(path, []byte(text), 0o600)
	}
	return nil
}

func (s *Store) listLocked(ctx context.Context) ([]Summary, error) {
	base := filepath.Join(s.root, plainDir)
	items := []Summary{}
	if _, err := os.Stat(base); errors.Is(err, fs.ErrNotExist) {
		return items, nil
	}
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != base && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return coded("PRIVATE_NOTE_SYMLINK_REJECTED", "private note symlinks are not allowed")
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		metadata, err := readMetadataFrontmatter(path)
		if err != nil {
			return fmt.Errorf("read private note metadata: %w", err)
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		items = append(items, summaryFromContent(filepath.ToSlash(rel), metadata, fileModTime(path)))
		return nil
	})
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, err
}

func (s *Store) resolveNote(raw, category, title string) (string, string, error) {
	rel, err := normalizeNotePath(raw, category, title)
	if err != nil {
		return "", "", err
	}
	abs, err := s.resolveInternal(rel)
	if err != nil {
		return "", "", err
	}
	return rel, abs, nil
}

func (s *Store) resolveInternal(rel string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(rel, "/")))
	if rel == "" || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", coded("INVALID_PRIVATE_NOTE_PATH", "private note path must be relative")
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", coded("INVALID_PRIVATE_NOTE_PATH", "private note path contains an invalid segment")
		}
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	abs := filepath.Clean(filepath.Join(s.root, clean))
	if abs == s.root || !strings.HasPrefix(abs, s.root+string(filepath.Separator)) {
		return "", coded("INVALID_PRIVATE_NOTE_PATH", "private note path escapes its root")
	}
	if err := ensureNoSymlink(s.root, filepath.ToSlash(clean)); err != nil {
		return "", err
	}
	return abs, nil
}

func normalizeNotePath(raw, category, title string) (string, error) {
	raw = strings.TrimSpace(raw)
	if filepath.IsAbs(filepath.FromSlash(raw)) || strings.HasPrefix(raw, "/") {
		return "", coded("INVALID_PRIVATE_NOTE_PATH", "private note path must be relative")
	}
	if raw == "" {
		category = safeSegment(category)
		if category == "" {
			category = "services"
		}
		title = slug(title)
		if title == "" {
			return "", coded("MISSING_PATH", "path or title is required")
		}
		raw = filepath.ToSlash(filepath.Join(plainDir, category, title+".md"))
	}
	raw = filepath.ToSlash(strings.TrimPrefix(raw, "/"))
	if !strings.HasPrefix(raw, plainDir+"/") {
		raw = plainDir + "/" + raw
	}
	raw = filepath.ToSlash(filepath.Clean(raw))
	if !strings.HasPrefix(raw, plainDir+"/") || !strings.HasSuffix(raw, ".md") {
		return "", coded("INVALID_PRIVATE_NOTE_PATH", "private note path must be a .md file under notes/")
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return "", coded("INVALID_PRIVATE_NOTE_PATH", "private note path contains an invalid segment")
		}
	}
	return raw, nil
}

func encryptedPath(notePath string) string {
	rel := strings.TrimPrefix(filepath.ToSlash(notePath), plainDir+"/")
	return filepath.ToSlash(filepath.Join(encryptedDir, rel+".age"))
}

func (s *Store) readIdentity() (*age.X25519Identity, error) {
	path := filepath.Join(s.root, keyDir, identityFile)
	if err := ensureRegularOrMissing(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, coded("PRIVATE_NOTES_AGE_IDENTITY_INVALID", "private notes age identity is invalid")
	}
	return identity, nil
}

func encryptedMatchesPlaintext(encryptedPath, plainPath string, identity age.Identity) (bool, error) {
	encrypted, err := os.Open(encryptedPath)
	if err != nil {
		return false, err
	}
	defer encrypted.Close()
	decrypted, err := age.Decrypt(encrypted, identity)
	if err != nil {
		return false, err
	}
	plain, err := os.Open(plainPath)
	if err != nil {
		return false, err
	}
	defer plain.Close()

	plainHash := sha256.New()
	plainBytes, err := io.Copy(plainHash, plain)
	if err != nil {
		return false, err
	}
	decryptedHash := sha256.New()
	decryptedBytes, err := io.Copy(decryptedHash, decrypted)
	if err != nil {
		return false, err
	}
	return plainBytes == decryptedBytes && bytes.Equal(plainHash.Sum(nil), decryptedHash.Sum(nil)), nil
}

func (s *Store) ensureIdentity() (*age.X25519Identity, bool, error) {
	path := filepath.Join(s.root, keyDir, identityFile)
	if err := ensureRegularOrMissing(path); err != nil {
		return nil, false, err
	}
	if data, err := os.ReadFile(path); err == nil {
		identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, false, coded("PRIVATE_NOTES_AGE_IDENTITY_INVALID", "private notes age identity is invalid")
		}
		if err := s.writeRecipient(identity.Recipient().String()); err != nil {
			return nil, false, err
		}
		return identity, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, false, err
	}
	if err := atomicWrite(path, []byte(identity.String()+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	if err := s.writeRecipient(identity.Recipient().String()); err != nil {
		return nil, false, err
	}
	return identity, true, nil
}

func (s *Store) writeRecipient(recipient string) error {
	path := filepath.Join(s.root, keyDir, recipientFile)
	if err := ensureRegularOrMissing(path); err != nil {
		return err
	}
	return atomicWrite(path, []byte(recipient+"\n"), 0o600)
}

func (s *Store) recipients() ([]age.Recipient, error) {
	path := filepath.Join(s.root, keyDir, recipientFile)
	if err := ensureRegularOrMissing(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, coded("PRIVATE_NOTES_AGE_RECIPIENT_MISSING", "private notes age recipient is missing; run init-encryption first")
	}
	if err != nil {
		return nil, err
	}
	values := strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' })
	recipients := make([]age.Recipient, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		recipient, err := age.ParseX25519Recipient(value)
		if err != nil {
			return nil, coded("PRIVATE_NOTES_AGE_RECIPIENT_INVALID", "private notes age recipient is invalid")
		}
		recipients = append(recipients, recipient)
	}
	if len(recipients) == 0 {
		return nil, coded("PRIVATE_NOTES_AGE_RECIPIENT_MISSING", "private notes age recipient is missing; run init-encryption first")
	}
	return recipients, nil
}

func encrypt(plain []byte, recipients []age.Recipient) ([]byte, error) {
	var encrypted bytes.Buffer
	writer, err := age.Encrypt(&encrypted, recipients...)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(writer, bytes.NewReader(plain)); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return encrypted.Bytes(), nil
}

func readMetadataFrontmatter(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(io.LimitReader(file, maxMetadataBytes+1))
	scanner.Buffer(make([]byte, 4096), maxMetadataBytes+1)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return "", nil
	}

	var builder strings.Builder
	builder.WriteString("---\n")
	bytesRead := len("---\n")
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += len(line) + 1
		if bytesRead > maxMetadataBytes {
			return "", coded("PRIVATE_NOTE_METADATA_TOO_LARGE", "private note frontmatter exceeds the metadata limit")
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
		if strings.TrimSpace(line) == "---" {
			return builder.String(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", coded("PRIVATE_NOTE_METADATA_INVALID", "private note frontmatter is not closed")
}

func summaryFromContent(rel, content string, modified time.Time) Summary {
	frontmatter := parseFrontmatter(content)
	category := ""
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 2 {
		category = parts[1]
	}
	title := unquote(frontmatter["title"])
	if title == "" {
		title = firstHeading(content)
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(rel), ".md")
	}
	updatedAt := unquote(frontmatter["updated_at"])
	if updatedAt == "" && !modified.IsZero() {
		updatedAt = modified.UTC().Format(time.RFC3339)
	}
	return Summary{
		Path: rel, EncryptedPath: encryptedPath(rel), Category: category,
		Title: title, Summary: unquote(frontmatter["summary"]), Tags: parseTags(frontmatter["tags"]),
		UpdatedAt: updatedAt, ContainsSecret: strings.EqualFold(unquote(frontmatter["contains_secret"]), "true"),
	}
}

func renderContent(rel string, req WriteRequest, previous string, existed bool) string {
	incoming := strings.TrimSpace(req.Content)
	incomingMeta, body, hasFrontmatter := splitFrontmatterDocument(incoming)
	if !hasFrontmatter {
		body = incoming
	}

	metadata := map[string]string{}
	if existed {
		for key, value := range parseFrontmatter(previous) {
			metadata[key] = value
		}
	}
	for key, value := range incomingMeta {
		metadata[key] = value
	}

	now := time.Now().UTC().Format(time.RFC3339)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = unquote(metadata["title"])
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(rel), ".md")
	}
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		summary = unquote(metadata["summary"])
	}
	tags := normalizeTags(req.Tags)
	if len(tags) == 0 {
		tags = parseTags(metadata["tags"])
	}
	createdAt := unquote(metadata["created_at"])
	if createdAt == "" {
		createdAt = now
	}
	containsSecret := strings.EqualFold(unquote(metadata["contains_secret"]), "true") || containsSecretMarker(body)

	controlled := map[string]bool{
		"title": true, "type": true, "contains_secret": true, "summary": true,
		"tags": true, "created_at": true, "updated_at": true,
	}
	unknownKeys := make([]string, 0, len(metadata))
	for key := range metadata {
		if !controlled[key] {
			unknownKeys = append(unknownKeys, key)
		}
	}
	sort.Strings(unknownKeys)

	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("title: " + yamlSingleLine(title) + "\n")
	builder.WriteString("type: private-note\n")
	builder.WriteString("contains_secret: ")
	if containsSecret {
		builder.WriteString("true\n")
	} else {
		builder.WriteString("false\n")
	}
	if summary != "" {
		builder.WriteString("summary: " + yamlSingleLine(summary) + "\n")
	}
	if len(tags) > 0 {
		builder.WriteString("tags: " + strings.Join(tags, ",") + "\n")
	}
	builder.WriteString("created_at: " + createdAt + "\n")
	builder.WriteString("updated_at: " + now + "\n")
	for _, key := range unknownKeys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			builder.WriteString(key + ": " + value + "\n")
		}
	}
	builder.WriteString("---\n\n")
	builder.WriteString(strings.TrimSpace(body))
	builder.WriteString("\n")
	return builder.String()
}

func splitFrontmatterDocument(content string) (map[string]string, string, bool) {
	if !strings.HasPrefix(content, "---\n") {
		return map[string]string{}, content, false
	}
	rest := content[4:]
	closing := strings.Index(rest, "\n---")
	if closing < 0 {
		return map[string]string{}, content, false
	}
	closingStart := 4 + closing + 1
	closingEnd := closingStart + 3
	if closingEnd < len(content) && content[closingEnd] != '\n' && content[closingEnd] != '\r' {
		return map[string]string{}, content, false
	}
	frontmatter := content[:closingEnd]
	body := strings.TrimLeft(content[closingEnd:], "\r\n")
	return parseFrontmatter(frontmatter), body, true
}

func parseFrontmatter(content string) map[string]string {
	values := map[string]string{}
	if !strings.HasPrefix(content, "---\n") {
		return values
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return values
	}
	for _, line := range strings.Split(content[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		if key != "" {
			values[key] = strings.TrimSpace(value)
		}
	}
	return values
}

func queryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_')
	})
	seen := map[string]bool{}
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" && !seen[field] {
			seen[field] = true
			terms = append(terms, field)
		}
	}
	return terms
}

func safeSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		case r == '-' || r == '_' || unicode.IsSpace(r) || r == '/':
			if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "-") {
				builder.WriteByte('-')
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func slug(value string) string { return safeSegment(value) }

func yamlSingleLine(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "'", "''")
	value = strings.ReplaceAll(value, "\n", " ")
	return "'" + value + "'"
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func parseTags(value string) []string {
	value = strings.TrimSpace(strings.Trim(value, "[]"))
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	for i := range parts {
		parts[i] = unquote(parts[i])
	}
	return normalizeTags(parts)
}

func unquote(value string) string {
	return strings.Trim(strings.TrimSpace(value), "\"'")
}

func firstHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func containsSecretMarker(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range []string{"password", "passwd", "token", "secret", "private key", "recovery code", "密码", "密钥", "恢复码", "验证码"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func truncateUTF8(value string, maxBytes int) (string, bool) {
	if maxBytes < 0 || len(value) <= maxBytes {
		return value, false
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end], true
}

func ensureTrailingNewline(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func ensureNoSymlink(root, rel string) error {
	root = filepath.Clean(root)
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return coded("PRIVATE_NOTE_SYMLINK_REJECTED", "private notes root must not be a symlink")
	}
	current := root
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return coded("PRIVATE_NOTE_SYMLINK_REJECTED", "private note symlinks are not allowed")
		}
	}
	return nil
}

func ensureRegularOrMissing(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return coded("PRIVATE_NOTE_UNSAFE_FILE", "private note key file must be a regular file")
	}
	return nil
}

func atomicWrite(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".private-note-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDirectoryBestEffort(filepath.Dir(path))
	return nil
}

func syncDirectoryBestEffort(path string) {
	dir, err := os.Open(path)
	if err != nil {
		return
	}
	_ = dir.Sync()
	_ = dir.Close()
}

func restoreFile(path string, previous []byte, existed bool, mode fs.FileMode) error {
	if existed {
		return atomicWrite(path, previous, mode)
	}
	return removeFile(path)
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	syncDirectoryBestEffort(filepath.Dir(path))
	return nil
}

func removeEmptyParents(dir, root string) {
	dir = filepath.Clean(dir)
	root = filepath.Clean(root)
	for dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func (s *Store) gitignoreContains(rule string) bool {
	content, err := os.ReadFile(filepath.Join(s.root, ".gitignore"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == rule {
			return true
		}
	}
	return false
}
