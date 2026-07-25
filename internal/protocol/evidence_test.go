package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvidenceStrictValidation(t *testing.T) {
	valid := Evidence{ID: "E-0001", TaskID: "T-0001", Kind: EvidenceDiff, Summary: "Source diff", FileRefs: []string{"patches/change.diff"}, CreatedBy: "cc-haha", CreatedAt: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeEvidence(data, "E-0001.json"); err != nil {
		t.Fatalf("DecodeEvidence() error = %v", err)
	}
	invalid := []Evidence{
		{ID: "E-0001", TaskID: "T-0001", Kind: EvidenceKind("unknown"), Summary: "x", CreatedBy: "cc-haha", CreatedAt: valid.CreatedAt},
		{ID: "E-0001", TaskID: "T-0001", Kind: EvidenceTest, Summary: "  ", CreatedBy: "cc-haha", CreatedAt: valid.CreatedAt},
		{ID: "E-0001", TaskID: "T-0001", Kind: EvidenceTest, Summary: "x", FileRefs: []string{"a", "a"}, CreatedBy: "cc-haha", CreatedAt: valid.CreatedAt},
		{ID: "E-0001", TaskID: "T-0001", Kind: EvidenceTest, Summary: "x", FileRefs: []string{"C:\\secret.txt"}, CreatedBy: "cc-haha", CreatedAt: valid.CreatedAt},
		{ID: "E-0001", TaskID: "T-0001", Kind: EvidenceTest, Summary: "x", FileRefs: []string{"token=hidden"}, CreatedBy: "cc-haha", CreatedAt: valid.CreatedAt},
	}
	for _, evidence := range invalid {
		if err := evidence.Validate("E-0001"); err == nil {
			t.Fatalf("invalid evidence accepted: %+v", evidence)
		}
	}
	if _, err := DecodeEvidence(append(data, []byte(`{"unknown":true}`)...), "E-0001.json"); err == nil {
		t.Fatal("multiple JSON values accepted")
	}
}

func TestTaskValidationRequiresCapabilities(t *testing.T) {
	task := Task{ID: "T-0001", ProjectID: "project-1", Title: "Title", Objective: "Objective", Acceptance: []string{"Pass"}, Creator: "cc-haha", Reviewer: "codex", CreatedAt: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)}
	if err := task.Validate(task.ID, testReferences{}); err == nil {
		t.Fatal("creator without create_task capability accepted")
	}
}
