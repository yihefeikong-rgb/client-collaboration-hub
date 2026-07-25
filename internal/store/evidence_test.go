package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

func TestEvidenceStoreCreatesImmutableEvidence(t *testing.T) {
	store := NewFileEvidenceStore(t.TempDir())
	evidence := protocol.Evidence{ID: "E-0001", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", FileRefs: []string{"patch.diff"}, CreatedBy: "cc-haha", CreatedAt: journalTime}
	created, err := store.EnsureEvidence(context.Background(), evidence)
	if err != nil || !created {
		t.Fatalf("EnsureEvidence() = %v, %v", created, err)
	}
	created, err = store.EnsureEvidence(context.Background(), evidence)
	if err != nil || created {
		t.Fatalf("idempotent EnsureEvidence() = %v, %v", created, err)
	}
	evidence.Summary = "Different"
	if _, err := store.EnsureEvidence(context.Background(), evidence); !errors.Is(err, ErrEvidenceConflict) {
		t.Fatalf("conflicting EnsureEvidence() error = %v", err)
	}
}

func TestEvidenceStoreRejectsUnknownFields(t *testing.T) {
	store := NewFileEvidenceStore(t.TempDir())
	if _, err := store.ReadEvidence(context.Background(), "T-0001", "E-0001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing evidence error = %v", err)
	}
}

func TestEvidenceStoreDoesNotPublishShortWrite(t *testing.T) {
	root := t.TempDir()
	store := NewFileEvidenceStore(root)
	store.FS = &faultFS{partialTemp: true}
	evidence := protocol.Evidence{ID: "E-0001", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "cc-haha", CreatedAt: journalTime}
	if _, err := store.EnsureEvidence(context.Background(), evidence); err == nil {
		t.Fatal("short evidence write accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "T-0001", "evidence", "E-0001.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published short evidence: %v", err)
	}
}
