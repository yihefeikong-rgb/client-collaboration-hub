package protocol

import (
	"strings"
	"testing"
)

type testReferences struct{}

func (testReferences) ProjectExists(id string) bool { return id == "project-1" }
func (testReferences) ClientExists(id string) bool {
	return id == "codex" || id == "cc-haha"
}

func TestDecodeTaskStrictValidation(t *testing.T) {
	task, err := DecodeTask([]byte(validTaskYAML), "T-0001.yaml", testReferences{})
	if err != nil {
		t.Fatalf("DecodeTask() error = %v", err)
	}
	if task.Reviewer != "codex" || task.ID != "T-0001" {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestDecodeTaskRejectsUnsafeOrInvalidYAML(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"unknown field", strings.Replace(validTaskYAML, "title: Fix", "unknown: nope\ntitle: Fix", 1)},
		{"duplicate key", strings.Replace(validTaskYAML, "title: Fix", "title: Fix\ntitle: Again", 1)},
		{"multiple documents", validTaskYAML + "---\nid: T-0002\n"},
		{"absolute path", strings.Replace(validTaskYAML, "objective: Repair", "objective: C:\\\\work", 1)},
		{"credential field", validTaskYAML + "token: secret\n"},
		{"non UTC timestamp", strings.Replace(validTaskYAML, "2026-07-24T14:00:00Z", "2026-07-24T22:00:00+08:00", 1)},
		{"unknown project", strings.Replace(validTaskYAML, "project-1", "project-2", 1)},
		{"path id mismatch", strings.Replace(validTaskYAML, "id: T-0001", "id: T-0002", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeTask([]byte(tt.content), "T-0001.yaml", testReferences{}); err == nil {
				t.Fatal("DecodeTask() error = nil")
			}
		})
	}
}

const validTaskYAML = `id: T-0001
project_id: project-1
title: Fix
objective: Repair
acceptance:
  - Tests pass
creator: codex
assignee: cc-haha
reviewer: codex
created_at: 2026-07-24T14:00:00Z
`
