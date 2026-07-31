package agentintake

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/handoff"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

var intakeTime = time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)

type finalizeFailsOnce struct {
	ReceiptStore
	failed bool
}

type partialSubmissionJournal struct {
	store.TaskJournal
}

func (j partialSubmissionJournal) ApplyAgentSubmission(ctx context.Context, taskID string, expectedVersion int64, input store.AgentSubmission) (store.AgentSubmissionResult, error) {
	input.Action = protocol.AddEvidence
	input.EvidenceRefs = nil
	result, err := j.TaskJournal.ApplyAgentSubmission(ctx, taskID, expectedVersion, input)
	if err != nil {
		return result, err
	}
	return result, errors.New("simulated journal failure after evidence")
}

func (s *finalizeFailsOnce) Finalize(ctx context.Context, receipt Receipt) (Receipt, error) {
	if !s.failed {
		s.failed = true
		return Receipt{}, errors.New("simulated receipt finalization failure")
	}
	return s.ReceiptStore.Finalize(ctx, receipt)
}

func TestSubmitResponseMovesWorkingTaskToReview(t *testing.T) {
	service, journal, input := newWorkingService(t)
	service.ValidateResponse = func(string, []byte) (handoff.ResponseValidation, error) {
		return validSubmissionResponse(3, 3), nil
	}
	result, err := service.SubmitResponse(context.Background(), "unused-package", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Accepted || result.State.Status != protocol.Review || result.State.Version != 6 {
		t.Fatalf("result = %#v", result)
	}
	if events, err := journal.Inspect(context.Background(), "TASK-1"); err != nil || events.State.LastEventID != 6 {
		t.Fatalf("state = %#v, %v", events, err)
	}
}

func TestSubmitResponseRejectsStaleCandidateWithoutTaskWrite(t *testing.T) {
	service, journal, input := newWorkingService(t)
	service.ValidateResponse = func(string, []byte) (handoff.ResponseValidation, error) {
		return validSubmissionResponse(2, 2), nil
	}
	before, err := journal.Inspect(context.Background(), "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SubmitResponse(context.Background(), "unused-package", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Rejected || result.Receipt.Reason == "" || result.State != before.State {
		t.Fatalf("result = %#v", result)
	}
	after, err := journal.Inspect(context.Background(), "TASK-1")
	if err != nil || after.State != before.State {
		t.Fatalf("after = %#v, %v", after, err)
	}
	receipts, err := service.Receipts.List(context.Background())
	if err != nil || len(receipts) != 1 || receipts[0].Status != Rejected {
		t.Fatalf("receipts = %#v, %v", receipts, err)
	}
}

func TestRejectedReceiptOmitsLocalCandidatePath(t *testing.T) {
	service, _, input := newWorkingService(t)
	localPath := filepath.Join(t.TempDir(), "missing-candidate.json")
	service.ValidateResponse = func(string, []byte) (handoff.ResponseValidation, error) {
		return handoff.ResponseValidation{}, &os.PathError{Op: "open", Path: localPath, Err: os.ErrNotExist}
	}
	result, err := service.SubmitResponse(context.Background(), "unused-package", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Rejected || strings.Contains(result.Receipt.Reason, localPath) || result.Receipt.Reason != "local candidate or package is unavailable" {
		t.Fatalf("receipt = %#v", result.Receipt)
	}
}

func TestSubmitResponseUsesCapturedCandidateBytesAfterFileReplacement(t *testing.T) {
	service, _, input := newWorkingService(t)
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	service.ValidateResponse = func(_ string, captured []byte) (handoff.ResponseValidation, error) {
		if !bytes.Equal(captured, original) {
			return handoff.ResponseValidation{}, errors.New("validator did not receive the captured candidate")
		}
		if err := os.WriteFile(input, []byte(`{"candidate":"replacement"}`), 0o600); err != nil {
			return handoff.ResponseValidation{}, err
		}
		return validSubmissionResponse(3, 3), nil
	}
	result, err := service.SubmitResponse(context.Background(), "unused-package", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Accepted || !bytes.Equal(result.Receipt.Raw, original) || result.Receipt.RawSHA256 != RawSHA256(original) {
		t.Fatalf("receipt = %#v", result.Receipt)
	}
	changed, err := os.ReadFile(input)
	if err != nil || bytes.Equal(changed, original) {
		t.Fatalf("candidate was not replaced during validation: %q, %v", changed, err)
	}
}

func TestSubmitResponseRecoversAcceptedReceiptAfterFinalizeFailure(t *testing.T) {
	service, journal, input := newWorkingService(t)
	service.ValidateResponse = func(string, []byte) (handoff.ResponseValidation, error) {
		return validSubmissionResponse(3, 3), nil
	}
	service.Receipts = &finalizeFailsOnce{ReceiptStore: service.Receipts}
	if _, err := service.SubmitResponse(context.Background(), "unused-package", input); err == nil {
		t.Fatal("submission unexpectedly succeeded when receipt finalization failed")
	}
	state, err := journal.Inspect(context.Background(), "TASK-1")
	if err != nil || state.State.Status != protocol.Review || state.State.Version != 6 {
		t.Fatalf("journal = %#v, %v", state, err)
	}
	result, err := service.SubmitResponse(context.Background(), "unused-package", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Accepted || result.State != state.State || len(result.Receipt.AppliedEventIDs) != 3 {
		t.Fatalf("reconciled result = %#v", result)
	}
}

func TestSubmitResponseKeepsPartialJournalApplicationUnknown(t *testing.T) {
	service, journal, input := newWorkingService(t)
	service.ValidateResponse = func(string, []byte) (handoff.ResponseValidation, error) {
		return validSubmissionResponse(3, 3), nil
	}
	service.Journal = partialSubmissionJournal{TaskJournal: journal}
	result, err := service.SubmitResponse(context.Background(), "unused-package", input)
	if err == nil {
		t.Fatal("partial journal application unexpectedly succeeded")
	}
	if result.Receipt.Status != Unknown || len(result.Receipt.AppliedEventIDs) != 0 || !strings.Contains(result.Receipt.Reason, "2 of 3") {
		t.Fatalf("receipt = %#v", result.Receipt)
	}
	state, inspectErr := journal.Inspect(context.Background(), "TASK-1")
	if inspectErr != nil || state.State.Status != protocol.Working || state.State.Version != 5 || state.State.LastEventID != 5 {
		t.Fatalf("partial state = %#v, %v", state, inspectErr)
	}
}

func TestSubmitResponseSerializesDuplicateCandidate(t *testing.T) {
	service, journal, input := newWorkingService(t)
	validationStarted := make(chan struct{}, 2)
	releaseValidation := make(chan struct{})
	service.ValidateResponse = func(string, []byte) (handoff.ResponseValidation, error) {
		validationStarted <- struct{}{}
		<-releaseValidation
		return validSubmissionResponse(3, 3), nil
	}
	type outcome struct {
		result Result
		err    error
	}
	results := make(chan outcome, 2)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		result, err := service.SubmitResponse(context.Background(), "unused-package", input)
		results <- outcome{result: result, err: err}
	}()
	<-validationStarted
	group.Add(1)
	go func() {
		defer group.Done()
		result, err := service.SubmitResponse(context.Background(), "unused-package", input)
		results <- outcome{result: result, err: err}
	}()
	select {
	case <-validationStarted:
		close(releaseValidation)
		group.Wait()
		t.Fatal("duplicate candidate bypassed the processing lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseValidation)
	group.Wait()
	close(results)
	for outcome := range results {
		if outcome.err != nil || outcome.result.Receipt.Status != Accepted {
			t.Fatalf("duplicate outcome = %#v", outcome)
		}
	}
	state, err := journal.Inspect(context.Background(), "TASK-1")
	if err != nil || state.State.Status != protocol.Review || state.State.Version != 6 {
		t.Fatalf("state = %#v, %v", state, err)
	}
}

func TestCreateTaskRecordsAgentProvenance(t *testing.T) {
	root := t.TempDir()
	registry := store.NewFileRegistryStore(root, store.FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: intakeTime}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review"}}); err != nil {
		t.Fatal(err)
	}
	journal := store.NewFileTaskJournal(root, store.FlockLocker{}, registry, store.NewFileEvidenceStore(root))
	service := NewService(registry, journal, store.NewFileTaskQuery(journal, registry), NewFileReceiptStore(root, store.FlockLocker{}), func() time.Time { return intakeTime })
	input := filepath.Join(root, "task.json")
	data := []byte(`{"format_version":"1","submission_id":"sub-create","source_client_id":"codex","id":"TASK-1","project_id":"project-1","title":"Task","objective":"Objective","acceptance":["Pass"],"creator":"codex","reviewer":"codex"}`)
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := service.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Accepted || result.State.Status != protocol.Draft || result.State.Version != 1 {
		t.Fatalf("result = %#v", result)
	}
	state, err := journal.Inspect(context.Background(), "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.State.LastEventID != 1 {
		t.Fatalf("state = %#v", state)
	}
}

func TestCreateTaskRecoversAcceptedReceiptAfterFinalizeFailure(t *testing.T) {
	root := t.TempDir()
	registry := store.NewFileRegistryStore(root, store.FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: intakeTime}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review"}}); err != nil {
		t.Fatal(err)
	}
	journal := store.NewFileTaskJournal(root, store.FlockLocker{}, registry, store.NewFileEvidenceStore(root))
	receipts := NewFileReceiptStore(root, store.FlockLocker{})
	service := NewService(registry, journal, store.NewFileTaskQuery(journal, registry), &finalizeFailsOnce{ReceiptStore: receipts}, func() time.Time { return intakeTime })
	input := filepath.Join(root, "task.json")
	data := []byte(`{"format_version":"1","submission_id":"sub-create","source_client_id":"codex","id":"TASK-1","project_id":"project-1","title":"Task","objective":"Objective","acceptance":["Pass"],"creator":"codex","reviewer":"codex"}`)
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(context.Background(), input); err == nil {
		t.Fatal("task creation unexpectedly succeeded when receipt finalization failed")
	}
	result, err := service.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Accepted || result.State.Status != protocol.Draft || result.State.Version != 1 || result.Receipt.AppliedEventIDs[0] != 1 {
		t.Fatalf("reconciled result = %#v", result)
	}
}

func TestCreateTaskRetainsMalformedCandidateAsRejectedReceipt(t *testing.T) {
	service, _, _ := newWorkingService(t)
	input := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(input, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := service.CreateTask(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.Status != Rejected || result.Receipt.Reason == "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestReceiptIDIgnoresJSONFormatting(t *testing.T) {
	compact := []byte(`{"candidate":"same","items":[1,2]}`)
	pretty := []byte("{\n  \"items\": [1, 2],\n  \"candidate\": \"same\"\n}\n")
	if ReceiptID(ResponseSubmission, compact) != ReceiptID(ResponseSubmission, pretty) {
		t.Fatal("same JSON candidate received different receipt ids")
	}
}

func newWorkingService(t *testing.T) (*Service, *store.FileTaskJournal, string) {
	t.Helper()
	root := t.TempDir()
	registry := store.NewFileRegistryStore(root, store.FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: intakeTime}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review", "import_export"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "cc-haha", Name: "CC-HAHA", Capabilities: []string{"execute", "import_export"}}); err != nil {
		t.Fatal(err)
	}
	journal := store.NewFileTaskJournal(root, store.FlockLocker{}, registry, store.NewFileEvidenceStore(root))
	task := protocol.Task{ID: "TASK-1", ProjectID: "project-1", Title: "Task", Objective: "Objective", Acceptance: []string{"Pass"}, Creator: "codex", Reviewer: "codex", CreatedAt: intakeTime}
	if err := journal.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), task.ID, 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: intakeTime}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), task.ID, 2, protocol.TransitionIntent{Action: protocol.Accept, Actor: "cc-haha", At: intakeTime}, nil); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "candidate.json")
	if err := os.WriteFile(input, []byte(`{"candidate":"raw"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	receipts := NewFileReceiptStore(root, store.FlockLocker{})
	service := NewService(registry, journal, store.NewFileTaskQuery(journal, registry), receipts, func() time.Time { return intakeTime })
	return service, journal, input
}

func validSubmissionResponse(version, eventID int64) handoff.ResponseValidation {
	return handoff.ResponseValidation{
		Manifest: handoff.Manifest{PackageID: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TaskID: "TASK-1"},
		Response: handoff.CandidateResponse{
			PackageID:            "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			TaskID:               "TASK-1",
			ObservedVersion:      version,
			ObservedThroughEvent: eventID,
			Actor:                "cc-haha",
			ProposedAction:       protocol.Submit,
			EvidenceRefs:         []string{"E-diff", "E-test"},
			Evidence: []handoff.CandidateEvidence{
				{ID: "E-diff", Kind: protocol.EvidenceDiff, Summary: "Diff", FileRefs: []string{"diff.patch"}},
				{ID: "E-test", Kind: protocol.EvidenceTest, Summary: "Tests", FileRefs: []string{"test.txt"}},
			},
		},
	}
}
