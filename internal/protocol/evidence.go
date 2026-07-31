package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

type Evidence struct {
	ID        string       `json:"id"`
	TaskID    string       `json:"task_id"`
	Kind      EvidenceKind `json:"kind"`
	Summary   string       `json:"summary"`
	FileRefs  []string     `json:"file_refs"`
	CreatedBy string       `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
}

type TransitionIntent struct {
	Action         Action
	Actor          string
	NextAssignee   string
	Feedback       string
	At             time.Time
	Origin         EventOrigin
	SubmissionID   string
	PolicyDecision string
}

func (e Evidence) Validate(expectedID string) error {
	if err := validateID("evidence id", e.ID, expectedID); err != nil {
		return err
	}
	if err := validateID("evidence task_id", e.TaskID, ""); err != nil {
		return err
	}
	if !isEvidenceKind(e.Kind) {
		return fmt.Errorf("invalid evidence kind %q", e.Kind)
	}
	if strings.TrimSpace(e.Summary) == "" {
		return fmt.Errorf("evidence summary is required")
	}
	if err := ValidatePortableText("evidence summary", e.Summary); err != nil {
		return err
	}
	if err := validateID("evidence created_by", e.CreatedBy, ""); err != nil {
		return err
	}
	if err := validateUTCTime("evidence created_at", e.CreatedAt); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, ref := range e.FileRefs {
		if seen[ref] {
			return fmt.Errorf("duplicate evidence file_ref")
		}
		if err := ValidatePortableFileRef(ref); err != nil {
			return err
		}
		seen[ref] = true
	}
	return nil
}

func DecodeEvidence(data []byte, path string) (Evidence, error) {
	var evidence Evidence
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return evidence, fmt.Errorf("decode evidence: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return evidence, fmt.Errorf("evidence contains multiple JSON values")
		}
		return evidence, fmt.Errorf("decode extra evidence value: %w", err)
	}
	if err := evidence.Validate(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func isEvidenceKind(kind EvidenceKind) bool {
	switch kind {
	case EvidenceDiff, EvidenceArtifact, EvidenceTest, EvidenceBlocker:
		return true
	default:
		return false
	}
}
