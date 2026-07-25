package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

var handoffTime = time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

type handoffFixture struct {
	service     *Service
	journal     *store.FileTaskJournal
	query       *store.FileTaskQuery
	bindings    *store.FileBindingStore
	projectPath string
	binding     store.ProjectBinding
}

func TestManualCCHahaHandoffIsPortableAndDeterministic(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	firstOutput := filepath.Join(t.TempDir(), "first")
	options := ExportOptions{TaskID: "T-0001", ClientID: "cc-haha", Adapter: "manual-cc-haha", DeviceID: "device-1", AfterEventID: 0, OutputDir: firstOutput}
	if _, err := fixture.service.Export(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	handoffData, err := os.ReadFile(filepath.Join(firstOutput, "handoff.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(firstOutput, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Adapter != "manual-cc-haha" || manifest.TargetClient != "cc-haha" || manifest.Status != protocol.Working || manifest.ThroughEvent != 5 || len(manifest.Events) != 5 || len(manifest.Evidence) != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}
	diffHash := sha256.Sum256([]byte("diff\n"))
	if !manifest.Evidence[0].Files[0].Available || manifest.Evidence[0].Files[0].SHA256 != hex.EncodeToString(diffHash[:]) {
		t.Fatalf("diff file = %+v", manifest.Evidence[0].Files[0])
	}
	if strings.Contains(string(handoffData), fixture.binding.LocalPath) || strings.Contains(string(manifestData), fixture.binding.LocalPath) {
		t.Fatal("binding local path leaked into package")
	}
	if !strings.Contains(string(handoffData), "collab task submit") || !strings.Contains(string(handoffData), "不会读取或控制 CC-HAHA") {
		t.Fatalf("manual-cc-haha content = %s", handoffData)
	}
	secondOutput := filepath.Join(t.TempDir(), "second")
	options.OutputDir = secondOutput
	if _, err := fixture.service.Export(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	secondManifest, err := os.ReadFile(filepath.Join(secondOutput, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondHandoff, err := os.ReadFile(filepath.Join(secondOutput, "handoff.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifestData) != string(secondManifest) || string(handoffData) != string(secondHandoff) {
		t.Fatal("handoff export is not deterministic")
	}
}

func TestManualCodexHandoffUsesReviewActionsAndCursor(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	if _, err := fixture.journal.CommitTransition(context.Background(), "T-0001", 5, protocol.TransitionIntent{Action: protocol.Submit, Actor: "cc-haha", At: handoffTime}, []string{"E-diff", "E-test"}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "review")
	if _, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "codex", Adapter: "manual-codex", DeviceID: "device-1", AfterEventID: 5, OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "handoff.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != protocol.Review || len(manifest.Events) != 1 || manifest.Events[0].EventID != 6 || strings.Contains(string(data), "collab task submit") || !strings.Contains(string(data), "collab review approve") || !strings.Contains(string(data), "collab review request-changes") {
		t.Fatalf("manual-codex handoff = %s", data)
	}
}

func TestHandoffRejectsUnsafePortableContentWithoutLeakingIt(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	snapshot, err := fixture.query.Snapshot(context.Background(), "T-0001", 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Task.Title = "token=do-not-print"
	target, err := fixture.service.Registry.ReadClient(context.Background(), "cc-haha")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.validatePortableSnapshot(context.Background(), snapshot, fixture.binding, target); err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatalf("unsafe scan error = %v", err)
	}
}

func TestHandoffOutputRefusesImplicitOverwrite(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	output := filepath.Join(t.TempDir(), "handoff")
	options := ExportOptions{TaskID: "T-0001", ClientID: "cc-haha", Adapter: "manual-cc-haha", DeviceID: "device-1", AfterEventID: 0, OutputDir: output}
	if _, err := fixture.service.Export(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Export(context.Background(), options); err == nil {
		t.Fatal("implicit overwrite accepted")
	}
	options.Force = true
	if _, err := fixture.service.Export(context.Background(), options); err != nil {
		t.Fatalf("explicit overwrite rejected: %v", err)
	}
}

func newWorkingHandoffFixture(t *testing.T) handoffFixture {
	t.Helper()
	root := t.TempDir()
	dataRoot := filepath.Join(root, "collaboration")
	registry := store.NewFileRegistryStore(dataRoot, store.FlockLocker{})
	project := protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: handoffTime}
	if err := registry.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	for _, client := range []protocol.Client{
		{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review"}},
		{ID: "cc-haha", Name: "CC-HAHA", Capabilities: []string{"execute"}},
	} {
		if err := registry.RegisterClient(context.Background(), client); err != nil {
			t.Fatal(err)
		}
	}
	evidenceStore := store.NewFileEvidenceStore(dataRoot)
	journal := store.NewFileTaskJournal(dataRoot, store.FlockLocker{}, registry, evidenceStore)
	task := protocol.Task{ID: "T-0001", ProjectID: project.ID, Title: "Fix task", Objective: "Verify handoff", Acceptance: []string{"Tests pass"}, Creator: "codex", Reviewer: "codex", CreatedAt: handoffTime}
	if err := journal.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), task.ID, 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: handoffTime}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), task.ID, 2, protocol.TransitionIntent{Action: protocol.Accept, Actor: "cc-haha", At: handoffTime}, nil); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(projectPath, "changes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "changes", "fix.diff"), []byte("diff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "reports", "test.txt"), []byte("tests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bindings := store.NewFileBindingStore(dataRoot, store.FlockLocker{}, registry)
	binding := store.ProjectBinding{DeviceID: "device-1", ProjectID: project.ID, LocalPath: projectPath, Revision: "r1", BoundAt: handoffTime}
	if err := bindings.BindProject(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	binding, err := bindings.ReadBinding(context.Background(), binding.DeviceID, binding.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []protocol.Evidence{
		{ID: "E-diff", TaskID: task.ID, Kind: protocol.EvidenceDiff, Summary: "Diff", FileRefs: []string{"changes/fix.diff"}, CreatedBy: "cc-haha", CreatedAt: handoffTime},
		{ID: "E-test", TaskID: task.ID, Kind: protocol.EvidenceTest, Summary: "Tests", FileRefs: []string{"reports/test.txt"}, CreatedBy: "cc-haha", CreatedAt: handoffTime},
	} {
		version := int64(3)
		if evidence.ID == "E-test" {
			version = 4
		}
		if _, err := journal.AddEvidence(context.Background(), task.ID, version, evidence); err != nil {
			t.Fatal(err)
		}
	}
	query := store.NewFileTaskQuery(journal, registry)
	service := NewService(query, bindings, store.NewFileBindingResolver(), registry)
	return handoffFixture{service: service, journal: journal, query: query, bindings: bindings, projectPath: projectPath, binding: binding}
}
