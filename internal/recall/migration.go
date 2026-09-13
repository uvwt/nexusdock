package recall

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type MigrationRequest struct {
	SourceRoot string `json:"source_root"`
	TargetRoot string `json:"target_root"`
	DryRun     bool   `json:"dry_run"`
}

type MigrationReport struct {
	SourceRoot string   `json:"source_root"`
	TargetRoot string   `json:"target_root"`
	Files      []string `json:"files"`
	FileCount  int      `json:"file_count"`
	TotalBytes int64    `json:"total_bytes"`
	DryRun     bool     `json:"dry_run"`
	Verified   bool     `json:"verified"`
	InPlace    bool     `json:"in_place"`
}

// MigrateRepository either validates an existing Recall repository in
// place or copies its visible data to a new root. Hidden metadata directories
// remain outside the migrated Recall content.
func MigrateRepository(req MigrationRequest) (MigrationReport, error) {
	source, err := filepath.Abs(req.SourceRoot)
	if err != nil {
		return MigrationReport{}, err
	}
	target, err := filepath.Abs(req.TargetRoot)
	if err != nil {
		return MigrationReport{}, err
	}
	report := MigrationReport{SourceRoot: source, TargetRoot: target, DryRun: req.DryRun, InPlace: source == target}
	info, err := os.Stat(source)
	if err != nil {
		return report, err
	}
	if !info.IsDir() {
		return report, errors.New("recall migration source must be a directory")
	}

	files, total, err := migrationInventory(source)
	if err != nil {
		return report, err
	}
	report.Files = files
	report.FileCount = len(files)
	report.TotalBytes = total
	if req.DryRun {
		return report, nil
	}
	if report.InPlace {
		store, err := NewStore(source)
		if err != nil {
			return report, err
		}
		if err := ValidateRepository(store); err != nil {
			return report, err
		}
		report.Verified = true
		return report, nil
	}

	if err := ensureMigrationTargetEmpty(target); err != nil {
		return report, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return report, err
	}
	for _, rel := range files {
		sourcePath := filepath.Join(source, filepath.FromSlash(rel))
		targetPath := filepath.Join(target, filepath.FromSlash(rel))
		if err := copyMigrationFile(sourcePath, targetPath); err != nil {
			return report, fmt.Errorf("copy %s: %w", rel, err)
		}
	}
	sourceSnapshot, err := SnapshotFiles(source)
	if err != nil {
		return report, err
	}
	targetSnapshot, err := SnapshotFiles(target)
	if err != nil {
		return report, err
	}
	if len(sourceSnapshot) != len(targetSnapshot) {
		return report, errors.New("recall migration verification failed: file count changed")
	}
	for path, digest := range sourceSnapshot {
		if targetSnapshot[path] != digest {
			return report, fmt.Errorf("recall migration verification failed: digest mismatch for %s", path)
		}
	}
	store, err := NewStore(target)
	if err != nil {
		return report, err
	}
	if err := ValidateRepository(store); err != nil {
		return report, err
	}
	report.Verified = true
	return report, nil
}

func migrationInventory(root string) ([]string, int64, error) {
	var files []string
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in recall repository: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		total += info.Size()
		return nil
	})
	sort.Strings(files)
	return files, total, err
}

func ensureMigrationTargetEmpty(target string) error {
	entries, err := os.ReadDir(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("recall migration target must be empty")
	}
	return nil
}

func copyMigrationFile(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
