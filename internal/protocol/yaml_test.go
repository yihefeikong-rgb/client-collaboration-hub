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
func (testReferences) ClientHasCapability(id, capability string) bool {
	return (id == "codex" && (capability == "create_task" || capability == "review")) ||
		(id == "cc-haha" && capability == "execute")
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

func TestDecodeProjectAndClientStrictValidation(t *testing.T) {
	if _, err := DecodeProject([]byte("id: project-1\nname: Demo\ncreated_at: 2026-07-24T14:00:00Z\n"), "project-1.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeClient([]byte("id: codex\nname: Codex\ncapabilities: [review, import_export]\n"), "codex.yaml"); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{
		"id: codex\nname: Codex\ncapabilities: []\n",
		"id: codex\nname: Codex\ncapabilities: [review, review]\n",
		"id: codex\nname: Codex\ncapabilities: [fly]\n",
		"id: codex\nname: Codex\ncapabilities: [review]\nunknown: true\n",
	} {
		if _, err := DecodeClient([]byte(content), "codex.yaml"); err == nil {
			t.Fatal("invalid client accepted")
		}
	}
	if _, err := DecodeProject([]byte("id: project-1\nname: Demo\ncreated_at: 2026-07-24T14:00:00Z\nunknown: true\n"), "project-1.yaml"); err == nil {
		t.Fatal("project unknown field accepted")
	}
}

func TestDecodeTaskRejectsUnsafeOrInvalidYAML(t *testing.T) {
	tests := []struct{ name, content string }{
		{"unknown field", strings.Replace(validTaskYAML, "title: Fix", "unknown: nope\ntitle: Fix", 1)},
		{"removed assignee", validTaskYAML + "assignee: cc-haha\n"},
		{"duplicate key", strings.Replace(validTaskYAML, "title: Fix", "title: Fix\ntitle: Again", 1)},
		{"multiple documents", validTaskYAML + "---\nid: T-0002\n"},
		{"windows path", strings.Replace(validTaskYAML, "objective: Repair", "objective: C:\\\\Users\\\\name", 1)},
		{"credential field", validTaskYAML + "access_token: secret\n"},
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

func TestDecodeTaskRejectsSupportedLocalFilesystemPaths(t *testing.T) {
	for _, path := range []string{
		"/home/name/project", "/Users/name/project", "/root/project", "/tmp/project", "/var/lib/project",
		"/etc/project", "/opt/project", "/usr/local/project", "/mnt/d/project", "/srv/project",
		"/workspace/project", "~/project", "file:///home/name/project",
	} {
		t.Run(path, func(t *testing.T) {
			content := strings.Replace(validTaskYAML, "objective: Repair", "objective: "+path, 1)
			if _, err := DecodeTask([]byte(content), "T-0001.yaml", testReferences{}); err == nil {
				t.Fatal("local filesystem path accepted")
			}
		})
	}
}

func TestDecodeTaskAllowsSafeWordsAndRoutes(t *testing.T) {
	content := strings.Replace(validTaskYAML, "objective: Repair", "objective: /health /api/v1/tasks token_budget session_strategy pathology", 1)
	if _, err := DecodeTask([]byte(content), "T-0001.yaml", testReferences{}); err != nil {
		t.Fatalf("safe text rejected: %v", err)
	}
}

const validTaskYAML = `id: T-0001
project_id: project-1
title: Fix
objective: Repair
acceptance:
  - Tests pass
creator: codex
reviewer: codex
created_at: 2026-07-24T14:00:00Z
`
