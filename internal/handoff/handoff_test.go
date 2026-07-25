package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if manifest.Adapter != "manual-cc-haha" || manifest.TargetClient != "cc-haha" || manifest.ActionActor != "cc-haha" || manifest.PackageID == "" || manifest.Status != protocol.Working || manifest.ThroughEvent != 5 || len(manifest.Events) != 5 || len(manifest.Evidence) != 2 {
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
	var repeated Manifest
	if err := json.Unmarshal(secondManifest, &repeated); err != nil || repeated.PackageID != manifest.PackageID {
		t.Fatalf("deterministic package ID = %+v, %v", repeated, err)
	}
	if err := os.WriteFile(filepath.Join(fixture.projectPath, "changes", "fix.diff"), []byte("changed diff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedOutput := filepath.Join(t.TempDir(), "changed")
	options.OutputDir = changedOutput
	if _, err := fixture.service.Export(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	changedData, err := os.ReadFile(filepath.Join(changedOutput, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var changed Manifest
	if err := json.Unmarshal(changedData, &changed); err != nil || changed.PackageID == manifest.PackageID {
		t.Fatalf("changed package ID = %+v, %v", changed, err)
	}
	if _, err := VerifyPackage(firstOutput); err != nil {
		t.Fatal(err)
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
	before, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Export(context.Background(), options); !errors.Is(err, ErrHandoffAlreadyExists) {
		t.Fatal("implicit overwrite accepted")
	}
	after, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("existing handoff changed after rejected export: %v", err)
	}
	if err := os.WriteFile(filepath.Join(output, "unexpected.txt"), []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPackage(output); err == nil {
		t.Fatal("package with extra file passed verification")
	}
}

func TestHandoffRejectsUnsafeAndExistingOutputsWithoutChangingThem(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	root := filepath.Dir(fixture.projectPath)
	options := ExportOptions{TaskID: "T-0001", ClientID: "cc-haha", Adapter: "manual-cc-haha", DeviceID: "device-1", AfterEventID: 0}
	for _, output := range []string{root, filepath.Join(root, "collaboration"), filepath.Join(root, "collaboration", "handoff")} {
		options.OutputDir = output
		if _, err := fixture.service.Export(context.Background(), options); !errors.Is(err, ErrHandoffUnsafeOutput) {
			t.Fatalf("unsafe output %q error = %v", output, err)
		}
	}
	file := filepath.Join(root, "existing.txt")
	directory := filepath.Join(root, "existing-dir")
	if err := os.WriteFile(file, []byte("keep-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(directory, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{file, directory} {
		options.OutputDir = output
		if _, err := fixture.service.Export(context.Background(), options); !errors.Is(err, ErrHandoffAlreadyExists) {
			t.Fatalf("existing output %q error = %v", output, err)
		}
	}
	if data, _ := os.ReadFile(file); string(data) != "keep-file" {
		t.Fatalf("existing file changed: %q", data)
	}
	if data, _ := os.ReadFile(sentinel); string(data) != "keep-dir" {
		t.Fatalf("existing directory changed: %q", data)
	}
	link := filepath.Join(root, "existing-link")
	if err := os.Symlink(directory, link); err == nil {
		options.OutputDir = link
		if _, err := fixture.service.Export(context.Background(), options); !errors.Is(err, ErrHandoffUnsafeOutput) {
			t.Fatalf("existing symlink error = %v", err)
		}
	}
}

func TestHandoffRequiresImportExportCapability(t *testing.T) {
	fixture := newWorkingHandoffFixtureWithCapabilities(t, []string{"create_task", "review", "import_export"}, []string{"execute"})
	_, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "cc-haha", Adapter: "manual-cc-haha", DeviceID: "device-1", AfterEventID: 0, OutputDir: filepath.Join(t.TempDir(), "handoff")})
	if err == nil || !strings.Contains(err.Error(), "import_export") {
		t.Fatalf("missing import_export was accepted: %v", err)
	}
}

func TestPackageIDChangesForCanonicalSnapshotInputs(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	output := filepath.Join(t.TempDir(), "handoff")
	if _, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "cc-haha", Adapter: "manual-cc-haha", DeviceID: "device-1", AfterEventID: 0, OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Manifest){
		"target":    func(value *Manifest) { value.TargetClient, value.ActionActor = "codex", "codex" },
		"revision":  func(value *Manifest) { value.ProjectRevision = "r2" },
		"version":   func(value *Manifest) { value.Version++ },
		"event":     func(value *Manifest) { value.Events[0].Body = "changed event" },
		"file hash": func(value *Manifest) { value.Evidence[0].Files[0].SHA256 = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := manifest
			changed.AllowedActions = append([]protocol.Action(nil), manifest.AllowedActions...)
			changed.Events = append([]protocol.Event(nil), manifest.Events...)
			changed.Evidence = append([]ManifestEvidence(nil), manifest.Evidence...)
			changed.Evidence[0].Files = append([]store.ResolvedFileRef(nil), manifest.Evidence[0].Files...)
			mutate(&changed)
			id, err := changed.ComputedPackageID()
			if err != nil || id == manifest.PackageID {
				t.Fatalf("package ID = %q, %v", id, err)
			}
		})
	}
}

func TestBlockedCreatorCanReceiveManualCodexHandoff(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	blocker := protocol.Evidence{ID: "E-block", TaskID: "T-0001", Kind: protocol.EvidenceBlocker, Summary: "Blocked", CreatedBy: "cc-haha", CreatedAt: handoffTime}
	if _, err := fixture.journal.AddEvidence(context.Background(), "T-0001", 5, blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CommitTransition(context.Background(), "T-0001", 6, protocol.TransitionIntent{Action: protocol.Block, Actor: "cc-haha", At: handoffTime}, []string{"E-block"}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "blocked")
	if _, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "codex", Adapter: "manual-codex", DeviceID: "device-1", AfterEventID: 0, OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := DecodeManifest(manifestData)
	if err != nil || manifest.Status != protocol.Blocked || manifest.ResponsibleClient != "cc-haha" || manifest.ActionActor != "codex" || !containsAction(manifest.AllowedActions, protocol.Assign) {
		t.Fatalf("blocked manifest = %+v, %v", manifest, err)
	}
	if err := fixture.service.Registry.RegisterClient(context.Background(), protocol.Client{ID: "other", Name: "Other", Capabilities: []string{"execute", "import_export"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "other", Adapter: "manual-codex", DeviceID: "device-1", AfterEventID: 0, OutputDir: filepath.Join(t.TempDir(), "other")}); err == nil {
		t.Fatal("unrelated client received a handoff")
	}
}

func TestDoneRejectsWritesButAllowsReadOnlyHandoff(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	if _, err := fixture.journal.CommitTransition(context.Background(), "T-0001", 5, protocol.TransitionIntent{Action: protocol.Submit, Actor: "cc-haha", At: handoffTime}, []string{"E-diff", "E-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CommitTransition(context.Background(), "T-0001", 6, protocol.TransitionIntent{Action: protocol.Approve, Actor: "codex", At: handoffTime}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.AppendMessage(context.Background(), "T-0001", 7, "codex", "late", handoffTime); err == nil {
		t.Fatal("DONE accepted a message")
	}
	if _, err := fixture.journal.AddEvidence(context.Background(), "T-0001", 7, protocol.Evidence{ID: "E-late", TaskID: "T-0001", Kind: protocol.EvidenceTest, Summary: "late", CreatedBy: "codex", CreatedAt: handoffTime}); err == nil {
		t.Fatal("DONE accepted evidence")
	}
	snapshot, err := fixture.query.SnapshotForActor(context.Background(), "T-0001", 0, "codex")
	if err != nil || len(snapshot.AllowedActions) != 0 {
		t.Fatalf("DONE snapshot = %+v, %v", snapshot, err)
	}
	output := filepath.Join(t.TempDir(), "done")
	if _, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "codex", Adapter: "manual-codex", DeviceID: "device-1", AfterEventID: 0, OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPackage(output); err != nil {
		t.Fatal(err)
	}
}

func TestResponseValidationIsReadOnlyAndChecksPackageIdentity(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	output := filepath.Join(t.TempDir(), "handoff")
	if _, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "cc-haha", Adapter: "manual-cc-haha", DeviceID: "device-1", AfterEventID: 0, OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	messagesPath := filepath.Join(filepath.Dir(fixture.projectPath), "collaboration", "tasks", "T-0001", "messages.jsonl")
	statePath := filepath.Join(filepath.Dir(fixture.projectPath), "collaboration", "tasks", "T-0001", "state.json")
	messagesBefore, err := os.ReadFile(messagesPath)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "candidate.json")
	template, err := os.ReadFile(filepath.Join(output, "candidate-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, template, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ValidateResponsePackage(output, input)
	if err != nil || !strings.Contains(result.CommandDraft, "collab task submit") {
		t.Fatalf("response validation = %+v, %v", result, err)
	}
	messagesAfter, _ := os.ReadFile(messagesPath)
	stateAfter, _ := os.ReadFile(statePath)
	if string(messagesBefore) != string(messagesAfter) || string(stateBefore) != string(stateAfter) {
		t.Fatal("response validation wrote task state")
	}
	response, err := DecodeCandidateResponse(template)
	if err != nil {
		t.Fatal(err)
	}
	response.PackageID = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	invalid, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateResponsePackage(output, input); err == nil {
		t.Fatal("mismatched package ID was accepted")
	}
	response, err = DecodeCandidateResponse(template)
	if err != nil {
		t.Fatal(err)
	}
	response.Evidence = []CandidateEvidence{{ID: "E-local", Kind: protocol.EvidenceDiff, Summary: "candidate", FileRefs: []string{"C:/local-only.diff"}}}
	unsafe, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, unsafe, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateResponsePackage(output, input); err == nil {
		t.Fatal("local candidate evidence reference was accepted")
	}
}

func TestPublisherReportsUnknownOutcomeAfterPostPublishVerification(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	root := filepath.Dir(fixture.projectPath)
	fixture.service.Publisher = DirectoryPublisher{WorkspaceRoot: root, Verify: func(string) error { return errors.New("verification failed") }}
	output := filepath.Join(root, "outcome-unknown")
	_, err := fixture.service.Export(context.Background(), ExportOptions{TaskID: "T-0001", ClientID: "cc-haha", Adapter: "manual-cc-haha", DeviceID: "device-1", AfterEventID: 0, OutputDir: output})
	if !errors.Is(err, ErrHandoffOutcomeUnknown) {
		t.Fatalf("publish error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(output, "manifest.json")); statErr != nil {
		t.Fatalf("published directory was removed after unknown outcome: %v", statErr)
	}
}

func TestHandoffRendersUntrustedContentAsData(t *testing.T) {
	fixture := newWorkingHandoffFixture(t)
	snapshot, err := fixture.query.SnapshotForActor(context.Background(), "T-0001", 0, "cc-haha")
	if err != nil {
		t.Fatal(err)
	}
	view, err := fixture.service.bindingView(context.Background(), fixture.binding, protocol.Client{ID: "cc-haha", Name: "CC-HAHA"}, snapshot.Evidence, snapshot.ActionActor)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Task.Title = "## injected heading ```"
	snapshot.Events[0].Body = "## injected event ```"
	view.Evidence[0].Evidence.Summary = "## injected evidence ```"
	packageData, err := (ManualCCHahaAdapter{}).Export(context.Background(), snapshot, view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(packageData.Handoff)
	if strings.Contains(text, "\n## injected") || strings.Contains(text, "\n```") {
		t.Fatalf("untrusted content escaped data rendering: %s", text)
	}
}

func newWorkingHandoffFixture(t *testing.T) handoffFixture {
	return newWorkingHandoffFixtureWithCapabilities(t, []string{"create_task", "review", "import_export"}, []string{"execute", "import_export"})
}

func newWorkingHandoffFixtureWithCapabilities(t *testing.T, codexCapabilities, ccCapabilities []string) handoffFixture {
	t.Helper()
	root := t.TempDir()
	dataRoot := filepath.Join(root, "collaboration")
	registry := store.NewFileRegistryStore(dataRoot, store.FlockLocker{})
	project := protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: handoffTime}
	if err := registry.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	for _, client := range []protocol.Client{
		{ID: "codex", Name: "Codex", Capabilities: codexCapabilities},
		{ID: "cc-haha", Name: "CC-HAHA", Capabilities: ccCapabilities},
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
	service := NewService(query, bindings, store.NewFileBindingResolver(), registry, root)
	return handoffFixture{service: service, journal: journal, query: query, bindings: bindings, projectPath: projectPath, binding: binding}
}
