package protocol

import (
	"testing"
	"time"
)

func TestDecodeProjectDefaultsToAgentAutoHumanFinal(t *testing.T) {
	project, err := DecodeProject([]byte("id: demo\nname: Demo\ncreated_at: 2026-07-28T00:00:00Z\n"), "demo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if project.CollaborationPolicy != DefaultCollaborationPolicy() {
		t.Fatalf("policy = %#v", project.CollaborationPolicy)
	}
	if project.PolicyVersion != 1 {
		t.Fatalf("policy version = %d", project.PolicyVersion)
	}
}

func TestDecodeProjectRejectsAutoDoneWithHumanFinal(t *testing.T) {
	_, err := DecodeProject([]byte("id: demo\nname: Demo\ncreated_at: 2026-07-28T00:00:00Z\ncollaboration_policy:\n  submission_mode: agent_auto\n  final_review: human\n  auto_done: true\n"), "demo.yaml")
	if err == nil {
		t.Fatal("expected invalid human-final policy to fail")
	}
}

func TestDecodeProjectRejectsIncompleteExplicitPolicyFields(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("id: demo\nname: Demo\ncreated_at: 2026-07-28T00:00:00Z\ncollaboration_policy: {}\n"),
		[]byte("id: demo\nname: Demo\ncreated_at: 2026-07-28T00:00:00Z\npolicy_version: 0\n"),
	} {
		if _, err := DecodeProject(data, "demo.yaml"); err == nil {
			t.Fatal("incomplete explicit policy accepted")
		}
	}
}

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
