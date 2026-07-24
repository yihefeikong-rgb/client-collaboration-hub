package protocol

import (
	"testing"
	"time"
)

func TestModelRejectsWhitespaceRequiredFields(t *testing.T) {
	createdAt := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	if err := (Project{ID: "project-1", Name: "  ", CreatedAt: createdAt}).Validate("project-1"); err == nil {
		t.Fatal("whitespace project name accepted")
	}
	if err := (Client{ID: "codex", Name: "  ", Capabilities: []string{"review"}}).Validate("codex"); err == nil {
		t.Fatal("whitespace client name accepted")
	}
	task := Task{ID: "T-0001", ProjectID: "project-1", Title: "  ", Objective: "  ", Acceptance: []string{"  "}, Creator: "codex", Reviewer: "codex", CreatedAt: createdAt}
	if err := task.Validate("T-0001", nil); err == nil {
		t.Fatal("whitespace task fields accepted")
	}
}
