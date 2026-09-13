package recall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type MutationEvent struct {
	Action   string    `json:"action"`
	Path     string    `json:"path"`
	Source   string    `json:"source,omitempty"`
	RunID    string    `json:"run_id,omitempty"`
	Occurred time.Time `json:"occurred_at"`
}

type MutationObserver interface {
	RecordMemoryMutation(context.Context, MutationEvent) error
}

type ServiceOption func(*Service)

func WithConflictRepository(repo ConflictRepository) ServiceOption {
	return func(s *Service) {
		if repo != nil {
			s.conflicts = repo
		}
	}
}

func WithMutationObserver(observer MutationObserver) ServiceOption {
	return func(s *Service) { s.observer = observer }
}

type Service struct {
	store     *Store
	conflicts ConflictRepository
	observer  MutationObserver
	now       func() time.Time
}

func NewService(store *Store, opts ...ServiceOption) (*Service, error) {
	if store == nil {
		return nil, errors.New("recall store is required")
	}
	svc := &Service{store: store, conflicts: NewInRecallConflictRepository(), now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

func (s *Service) Read(_ context.Context, path string) (Record, error) {
	mem, err := s.store.Read(path)
	if err != nil {
		return Record{}, err
	}
	return recordFromRecall(mem), nil
}

func (s *Service) Search(ctx context.Context, req SearchRequest) ([]Record, error) {
	results, err := s.store.Search(req.Query, req.Prefix, req.MaxResults)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(results))
	for _, result := range results {
		record, err := s.Read(ctx, result.Path)
		if err != nil {
			continue
		}
		if matchesMetadata(record.Metadata, req.Scopes, req.Statuses, req.Project, req.Device, req.Agent, req.Skill) {
			out = append(out, record)
		}
	}
	return out, nil
}

func (s *Service) List(ctx context.Context, req ListRequest) ([]Record, error) {
	entries, err := s.store.List(req.Prefix, req.MaxEntries)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "file" || !IsTextFile(entry.Path) {
			continue
		}
		record, err := s.Read(ctx, entry.Path)
		if err != nil {
			continue
		}
		if matchesMetadata(record.Metadata, req.Scopes, req.Statuses, req.Project, req.Device, req.Agent, req.Skill) {
			out = append(out, record)
		}
	}
	return out, nil
}

func (s *Service) DetectConflict(ctx context.Context, req DetectConflictRequest) ([]RecallConflict, error) {
	out := make([]RecallConflict, 0, len(req.Facts))
	now := s.now()
	for _, fact := range req.Facts {
		conflict, ok := conflictFromFact(fact, now)
		if !ok {
			continue
		}
		if _, err := s.store.Read(conflict.RecallPath); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if err := s.conflicts.Upsert(ctx, conflict); err != nil {
			return nil, err
		}
		out = append(out, conflict)
	}
	return out, nil
}

func (s *Service) ProposeUpdate(_ context.Context, req ProposeUpdateRequest) (UpdateProposal, error) {
	if err := validateProposal(req); err != nil {
		return UpdateProposal{}, err
	}
	var existing Recall
	var previous string
	mem, err := s.store.Read(req.Path)
	if err == nil {
		existing = mem
		previous = digestString(mem.Content)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return UpdateProposal{}, err
	}
	meta := Metadata{
		Scope: req.Scope, Status: req.Status, Project: req.Project, Device: req.Device, Agent: req.Agent,
		Skill: req.Skill, Source: req.Source,
		Verification: VerificationMetadata{
			VerifiedAt: req.VerifiedAt, VerificationRunID: req.VerificationRunID,
			SourceDevice: req.SourceDevice, SourceAgent: req.SourceAgent, Confidence: req.Confidence,
		},
	}
	content := renderFrontmatter(req.Content, meta, existing.Frontmatter)
	proposedDigest := digestString(content)
	return UpdateProposal{
		ID: proposalID(req.Path, proposedDigest), Path: req.Path, PreviousDigest: previous,
		ProposedDigest: proposedDigest, ProposedContent: content, Metadata: meta,
		Diff: simpleUnifiedDiff(req.Path, existing.Content, content), CreatedAt: s.now(),
	}, nil
}

func (s *Service) ApplyUpdate(ctx context.Context, req ApplyUpdateRequest) (Record, error) {
	if !req.Approved {
		return Record{}, errors.New("recall update requires explicit approval")
	}
	proposal := req.Proposal
	if proposal.ID == "" || proposal.ID != proposalID(proposal.Path, proposal.ProposedDigest) {
		return Record{}, errors.New("invalid recall proposal id")
	}
	if digestString(proposal.ProposedContent) != proposal.ProposedDigest {
		return Record{}, errors.New("recall proposal content digest mismatch")
	}
	current, err := s.store.Read(proposal.Path)
	if err == nil {
		if proposal.PreviousDigest == "" || digestString(current.Content) != proposal.PreviousDigest {
			return Record{}, errors.New("recall changed since proposal was created")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Record{}, err
	} else if proposal.PreviousDigest != "" {
		return Record{}, errors.New("recall disappeared since proposal was created")
	}
	mem, err := s.store.Write(WriteRequest{
		Path: proposal.Path, Content: proposal.ProposedContent, Confirmed: true, Overwrite: true,
	})
	if err != nil {
		return Record{}, err
	}
	if s.observer != nil {
		if err := s.observer.RecordMemoryMutation(ctx, MutationEvent{
			Action: "recall.update.applied", Path: proposal.Path, Source: proposal.Metadata.Source,
			RunID: proposal.Metadata.Verification.VerificationRunID, Occurred: s.now(),
		}); err != nil {
			return Record{}, fmt.Errorf("record recall mutation: %w", err)
		}
	}
	return recordFromRecall(mem), nil
}

func recordFromRecall(mem Recall) Record {
	return Record{Recall: mem, Metadata: MetadataFromRecall(mem)}
}

func verifiedTime(meta Metadata) time.Time {
	if meta.Verification.VerifiedAt == nil {
		return time.Time{}
	}
	return *meta.Verification.VerifiedAt
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

// ValidateRepository confirms that every visible recall entry can be read without
// mutating it. Migration uses this before and after copying an old repository.
func ValidateRepository(store *Store) error {
	entries, err := store.List("", 1000)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type != "file" || !IsTextFile(entry.Path) {
			continue
		}
		if _, err := store.Read(entry.Path); err != nil {
			return fmt.Errorf("validate %s: %w", entry.Path, err)
		}
	}
	return nil
}

// SnapshotFiles returns path and content digests for lossless migration checks.
func SnapshotFiles(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = digestString(string(data))
		return nil
	})
	return out, err
}
