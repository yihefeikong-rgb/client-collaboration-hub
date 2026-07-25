package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

var ErrEvidenceConflict = errors.New("evidence conflict")

type EvidenceStore interface {
	EnsureEvidence(context.Context, protocol.Evidence) (bool, error)
	ReadEvidence(context.Context, string, string) (protocol.Evidence, error)
	ResolveEvidence(string, string) (protocol.Evidence, error)
}

type FileEvidenceStore struct {
	Root     string
	FS       FileSystem
	Replacer AtomicReplacer
}

func NewFileEvidenceStore(root string) *FileEvidenceStore {
	return &FileEvidenceStore{Root: root, FS: osFileSystem{}, Replacer: osReplacer{}}
}

func (s *FileEvidenceStore) EnsureEvidence(ctx context.Context, evidence protocol.Evidence) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := evidence.Validate(evidence.ID); err != nil {
		return false, err
	}
	path := s.evidencePath(evidence.TaskID, evidence.ID)
	_, err := s.FS.Stat(path)
	if err == nil {
		existing, readErr := s.ReadEvidence(ctx, evidence.TaskID, evidence.ID)
		if readErr != nil {
			return false, readErr
		}
		if reflect.DeepEqual(existing, evidence) {
			return false, nil
		}
		return false, fmt.Errorf("%w: evidence %q", ErrEvidenceConflict, evidence.ID)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return false, err
	}
	if err := writeAtomically(s.FS, s.Replacer, path, ".evidence-*.tmp", append(data, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

func (s *FileEvidenceStore) ReadEvidence(_ context.Context, taskID, evidenceID string) (protocol.Evidence, error) {
	var evidence protocol.Evidence
	if !protocol.IsValidID(taskID) || !protocol.IsValidID(evidenceID) {
		return evidence, fmt.Errorf("invalid task or evidence id")
	}
	data, err := s.FS.ReadFile(s.evidencePath(taskID, evidenceID))
	if errors.Is(err, os.ErrNotExist) {
		return evidence, fmt.Errorf("%w: evidence %q", ErrNotFound, evidenceID)
	}
	if err != nil {
		return evidence, err
	}
	evidence, err = protocol.DecodeEvidence(data, evidenceID+".json")
	if err != nil {
		return evidence, err
	}
	if evidence.TaskID != taskID {
		return evidence, fmt.Errorf("evidence task_id mismatch")
	}
	return evidence, nil
}

func (s *FileEvidenceStore) ResolveEvidence(taskID, evidenceID string) (protocol.Evidence, error) {
	return s.ReadEvidence(context.Background(), taskID, evidenceID)
}

func (s *FileEvidenceStore) evidencePath(taskID, evidenceID string) string {
	return filepath.Join(s.Root, "tasks", taskID, "evidence", evidenceID+".json")
}
