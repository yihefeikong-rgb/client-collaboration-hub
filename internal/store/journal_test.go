package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

var journalTime = time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

func TestCreateTaskCreatesAuditableInitialState(t *testing.T) {
	journal, root := newJournal(t)
	if err := journal.CreateTask(context.Background(), testTask("T-0001")); err != nil {
		t.Fatal(err)
	}
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Healthy || report.State.Version != 1 || report.State.LastEventID != 1 || report.State.Status != protocol.Draft {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "T-0001", "task.yaml")); err != nil {
		t.Fatal(err)
	}
	if event := readEvents(t, root, "T-0001")[0]; !event.At.Equal(testTask("T-0001").CreatedAt) || event.Type != protocol.EventTaskCreated {
		t.Fatalf("initial event = %+v", event)
	}
	if err := journal.CreateTask(context.Background(), testTask("T-0001")); err == nil {
		t.Fatal("duplicate task accepted")
	}
}

func TestCommitTransitionDerivesEventAndState(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	next, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil)
	if err != nil || next.Status != protocol.Assigned || next.Version != 2 || next.AssignedClient != "cc-haha" {
		t.Fatalf("CommitTransition() = %+v, %v", next, err)
	}
	event := readEvents(t, journal.Root, "T-0001")[1]
	if event.Type != protocol.EventAssigned || event.TargetClient != "cc-haha" || event.EventID != 2 || event.ExpectedVersion != 1 {
		t.Fatalf("event = %+v", event)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 2, protocol.TransitionIntent{Action: protocol.Approve, Actor: "codex", At: journalTime}, nil); err == nil {
		t.Fatal("illegal transition accepted")
	}
}

func TestAssignRequiresRegisteredExecutingTarget(t *testing.T) {
	journal, root := newJournal(t)
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "review-only", Name: "Review only", Capabilities: []string{"review"}}); err != nil {
		t.Fatal(err)
	}
	createTask(t, journal, "T-0001")
	for _, target := range []string{"ghost-client", "review-only"} {
		if _, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: target, At: journalTime}, nil); err == nil {
			t.Fatalf("assignment to %q accepted", target)
		}
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatalf("assignment to executor rejected: %v", err)
	}
}

func TestAppendMessagePreservesBusinessState(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	next, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime)
	if err != nil || next.Status != protocol.Draft || next.ResponsibleClient != "codex" || next.Version != 2 || next.LastEventID != 2 {
		t.Fatalf("AppendMessage() = %+v, %v", next, err)
	}
	if event := readEvents(t, journal.Root, "T-0001")[1]; event.Type != protocol.EventMessageAdded || event.Body != "hello" {
		t.Fatalf("event = %+v", event)
	}
}

func TestJournalAndQueryShareActionPolicy(t *testing.T) {
	journal, root := newJournal(t)
	createTask(t, journal, "T-0001")
	query := NewFileTaskQuery(journal, NewFileRegistryStore(root, FlockLocker{}))
	snapshot, err := query.SnapshotForActor(context.Background(), "T-0001", 0, "codex")
	if err != nil || !containsStoreAction(snapshot.AllowedActions, protocol.Assign) {
		t.Fatalf("creator snapshot = %+v, %v", snapshot, err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err = query.SnapshotForActor(context.Background(), "T-0001", 0, "cc-haha")
	if err != nil || !containsStoreAction(snapshot.AllowedActions, protocol.Accept) {
		t.Fatalf("executor snapshot = %+v, %v", snapshot, err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 2, protocol.TransitionIntent{Action: protocol.Accept, Actor: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err = query.SnapshotForActor(context.Background(), "T-0001", 0, "codex")
	if err != nil || containsStoreAction(snapshot.AllowedActions, protocol.Submit) {
		t.Fatalf("creator working snapshot = %+v, %v", snapshot, err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 3, protocol.TransitionIntent{Action: protocol.Submit, Actor: "codex", At: journalTime}, nil); err == nil {
		t.Fatal("Journal accepted an action absent from actor allowed_actions")
	}
}

func containsStoreAction(actions []protocol.Action, wanted protocol.Action) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}

func TestAddEvidenceDerivesSubmissionKindsAndPreservesBusinessState(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	diff := protocol.Evidence{ID: "E-diff", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: journalTime}
	added, err := journal.AddEvidence(context.Background(), "T-0001", 1, diff)
	if err != nil || !added.Changed || added.State.Status != protocol.Draft || added.State.Version != 2 || added.State.LastEventID != 2 {
		t.Fatalf("AddEvidence() = %+v, %v", added, err)
	}
	testEvidence := protocol.Evidence{ID: "E-test", TaskID: "T-0001", Kind: protocol.EvidenceTest, Summary: "Tests", CreatedBy: "codex", CreatedAt: journalTime}
	if _, err := journal.AddEvidence(context.Background(), "T-0001", 2, testEvidence); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 3, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 4, protocol.TransitionIntent{Action: protocol.Accept, Actor: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 5, protocol.TransitionIntent{Action: protocol.Submit, Actor: "cc-haha", At: journalTime}, []string{"E-missing"}); err == nil {
		t.Fatal("missing evidence accepted")
	}
	next, err := journal.CommitTransition(context.Background(), "T-0001", 5, protocol.TransitionIntent{Action: protocol.Submit, Actor: "cc-haha", At: journalTime}, []string{"E-diff", "E-test"})
	if err != nil || next.Status != protocol.Review || next.Version != 6 {
		t.Fatalf("CommitTransition() = %+v, %v", next, err)
	}
}

func TestAddEvidenceCanResumeFromOrphanAfterRecovery(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	evidence := protocol.Evidence{ID: "E-0001", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: journalTime}
	journal.Replacer = failingReplacer{}
	if _, err := journal.AddEvidence(context.Background(), "T-0001", 1, evidence); err == nil {
		t.Fatal("replace failure accepted")
	}
	journal.Replacer = osReplacer{}
	if _, err := journal.RecoverTail(context.Background(), "T-0001"); err != nil {
		t.Fatal(err)
	}
	added, err := journal.AddEvidence(context.Background(), "T-0001", 1, evidence)
	if err != nil || !added.Changed || added.State.Version != 2 || added.State.Status != protocol.Draft {
		t.Fatalf("AddEvidence() = %+v, %v", added, err)
	}
}

func TestAddEvidenceIsJournalIdempotent(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	evidence := protocol.Evidence{ID: "E-0001", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: journalTime}
	first, err := journal.AddEvidence(context.Background(), "T-0001", 1, evidence)
	if err != nil || !first.Changed || first.State.Version != 2 {
		t.Fatalf("first AddEvidence() = %+v, %v", first, err)
	}
	second, err := journal.AddEvidence(context.Background(), "T-0001", 1, evidence)
	if err != nil || second.Changed || second.State.Version != first.State.Version || second.State.LastEventID != first.State.LastEventID {
		t.Fatalf("idempotent AddEvidence() = %+v, %v", second, err)
	}
}

func TestCommitTransitionRejectsUnannouncedEvidence(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 2, protocol.TransitionIntent{Action: protocol.Accept, Actor: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	diff := protocol.Evidence{ID: "E-diff", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "cc-haha", CreatedAt: journalTime}
	testEvidence := protocol.Evidence{ID: "E-test", TaskID: "T-0001", Kind: protocol.EvidenceTest, Summary: "Tests", CreatedBy: "cc-haha", CreatedAt: journalTime}
	for _, evidence := range []protocol.Evidence{diff, testEvidence} {
		if _, err := journal.Evidence.EnsureEvidence(context.Background(), evidence); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 3, protocol.TransitionIntent{Action: protocol.Submit, Actor: "cc-haha", At: journalTime}, []string{diff.ID, testEvidence.ID}); err == nil {
		t.Fatal("unannounced evidence was accepted")
	}
}

func TestInspectRejectsDuplicateEvidenceAnnouncement(t *testing.T) {
	journal, root := newJournal(t)
	createTask(t, journal, "T-0001")
	evidence := protocol.Evidence{ID: "E-0001", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: journalTime}
	if _, err := journal.AddEvidence(context.Background(), "T-0001", 1, evidence); err != nil {
		t.Fatal(err)
	}
	events := readEvents(t, root, "T-0001")
	duplicate := events[1]
	duplicate.EventID = 3
	duplicate.ExpectedVersion = 2
	events = append(events, duplicate)
	writeEvents(t, root, "T-0001", events)
	writeState(t, root, protocol.State{TaskID: "T-0001", Status: protocol.Draft, Version: 3, LastEventID: 3, ResponsibleClient: "codex", UpdatedAt: journalTime})
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Corrupt {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
}

func TestAppendMessageRejectsUnrelatedClient(t *testing.T) {
	journal, root := newJournal(t)
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "observer", Name: "Observer", Capabilities: []string{"import_export"}}); err != nil {
		t.Fatal(err)
	}
	createTask(t, journal, "T-0001")
	if _, err := journal.AppendMessage(context.Background(), "T-0001", 1, "observer", "hello", journalTime); err == nil {
		t.Fatal("unrelated client message accepted")
	}
}

func TestInspectAllowsUnreferencedEvidenceAndSeparatesMissingTask(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	evidence := protocol.Evidence{ID: "E-0001", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Orphan", CreatedBy: "codex", CreatedAt: journalTime}
	if _, err := journal.Evidence.EnsureEvidence(context.Background(), evidence); err != nil {
		t.Fatal(err)
	}
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Healthy {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
	if _, err := journal.Inspect(context.Background(), "T-404"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing task error = %v", err)
	}
}

func TestInspectReplayRejectsIllegalHistory(t *testing.T) {
	journal, root := newJournal(t)
	createTask(t, journal, "T-0001")
	state := protocol.State{TaskID: "T-0001", Status: protocol.Done, Version: 2, LastEventID: 2, AssignedClient: "cc-haha", ResponsibleClient: "codex", UpdatedAt: journalTime}
	writeState(t, root, state)
	events := append(readEvents(t, root, "T-0001"), protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventApproved, Actor: "codex", At: journalTime, ExpectedVersion: 1})
	writeEvents(t, root, "T-0001", events)
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Corrupt {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
}

func TestCommitSerializesSameTask(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		}
		if errors.Is(err, ErrVersionConflict) {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestInspectRejectsInvalidAuditChain(t *testing.T) {
	tests := []struct {
		name  string
		state protocol.State
		event protocol.Event
	}{
		{"time moves backward", protocol.State{TaskID: "T-0001", Status: protocol.Draft, Version: 2, LastEventID: 2, ResponsibleClient: "codex", UpdatedAt: journalTime}, protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventMessageAdded, Actor: "codex", At: journalTime.Add(-time.Second), Body: "bad", ExpectedVersion: 1}},
		{"duplicate task_created", protocol.State{TaskID: "T-0001", Status: protocol.Draft, Version: 2, LastEventID: 2, ResponsibleClient: "codex", UpdatedAt: journalTime}, protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventTaskCreated, Actor: "codex", At: journalTime, Body: "Test", ExpectedVersion: 1}},
		{"assigned target differs from state", protocol.State{TaskID: "T-0001", Status: protocol.Assigned, Version: 2, LastEventID: 2, AssignedClient: "other-client", ResponsibleClient: "other-client", UpdatedAt: journalTime}, protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventAssigned, Actor: "codex", At: journalTime, TargetClient: "cc-haha", ExpectedVersion: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal, root := newJournal(t)
			createTask(t, journal, "T-0001")
			writeState(t, root, test.state)
			events := append(readEvents(t, root, "T-0001"), test.event)
			writeEvents(t, root, "T-0001", events)
			report, err := journal.Inspect(context.Background(), "T-0001")
			if err != nil || report.Health != Corrupt {
				t.Fatalf("Inspect() = %+v, %v", report, err)
			}
		})
	}
}

func TestInspectRejectsTamperedAssignmentTargetReferences(t *testing.T) {
	for _, target := range []string{"ghost-client", "codex"} {
		t.Run(target, func(t *testing.T) {
			journal, root := newJournal(t)
			createTask(t, journal, "T-0001")
			state := protocol.State{TaskID: "T-0001", Status: protocol.Assigned, Version: 2, LastEventID: 2, AssignedClient: target, ResponsibleClient: target, UpdatedAt: journalTime}
			writeState(t, root, state)
			events := append(readEvents(t, root, "T-0001"), protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventAssigned, Actor: "codex", At: journalTime, TargetClient: target, ExpectedVersion: 1})
			writeEvents(t, root, "T-0001", events)
			report, err := journal.Inspect(context.Background(), "T-0001")
			if err != nil || report.Health != Corrupt {
				t.Fatalf("Inspect() = %+v, %v", report, err)
			}
		})
	}
}

func TestRecoverTailRestoresPriorState(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	journal.Replacer = failingReplacer{}
	_, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil)
	if err == nil {
		t.Fatal("replace failure accepted")
	}
	journal.Replacer = osReplacer{}
	before, _ := journal.Inspect(context.Background(), "T-0001")
	if before.Health != RecoverableTail {
		t.Fatalf("before = %+v", before)
	}
	recovered, err := journal.RecoverTail(context.Background(), "T-0001")
	if err != nil || recovered.After.Health != Healthy || recovered.After.State != before.State || recovered.BackupPath == "" {
		t.Fatalf("RecoverTail() = %+v, %v", recovered, err)
	}
}

func TestFaultInjectionShortWritesAndOutcomeUnknown(t *testing.T) {
	journal, _ := newJournal(t)
	fs := &faultFS{}
	journal.FS = fs
	createTask(t, journal, "T-0001")
	fs.partialTemp = true
	_, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime)
	if err == nil {
		t.Fatal("state short write accepted")
	}
	fs.disableFaults()
	report, _ := journal.Inspect(context.Background(), "T-0001")
	if report.Health != RecoverableTail {
		t.Fatalf("report = %+v", report)
	}
	fs.partialBackup = true
	recovery, err := journal.RecoverTail(context.Background(), "T-0001")
	if err == nil || recovery.BackupError == "" {
		t.Fatal("backup short write accepted")
	}
	fs.disableFaults()
	report, _ = journal.Inspect(context.Background(), "T-0001")
	if report.Health != RecoverableTail {
		t.Fatalf("tail was truncated after failed backup: %+v", report)
	}
}

func TestCommitReturnsOutcomeUnknownAfterReplace(t *testing.T) {
	journal, _ := newJournal(t)
	fs := &faultFS{}
	journal.FS = fs
	createTask(t, journal, "T-0001")
	journal.Replacer = replaceThenBreakReader{fs: fs}
	if _, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime); !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	fs.failRead = false
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Healthy || report.State.Version != 2 {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
}

func TestCreateTaskDoesNotPublishHalfInitializedDirectory(t *testing.T) {
	journal, root := newJournal(t)
	journal.FS = &faultFS{partialNew: true}
	if err := journal.CreateTask(context.Background(), testTask("T-0001")); err == nil {
		t.Fatal("short task initialization write accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "T-0001")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published half-initialized task: %v", err)
	}
}

func TestApplyAgentSubmissionAddsEvidenceAndTransitionsToReview(t *testing.T) {
	journal, root := newJournal(t)
	createTask(t, journal, "T-0001")
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionIntent{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 2, protocol.TransitionIntent{Action: protocol.Accept, Actor: "cc-haha", At: journalTime}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := journal.ApplyAgentSubmission(context.Background(), "T-0001", 3, AgentSubmission{
		ID:       "sub-001",
		Actor:    "cc-haha",
		Decision: protocol.PolicyDecisionAgentAutoHumanFinal,
		Action:   protocol.Submit,
		Evidence: []protocol.Evidence{
			{ID: "E-diff", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "cc-haha", CreatedAt: journalTime},
			{ID: "E-test", TaskID: "T-0001", Kind: protocol.EvidenceTest, Summary: "Tests", CreatedBy: "cc-haha", CreatedAt: journalTime},
		},
		EvidenceRefs: []string{"E-diff", "E-test"},
		At:           journalTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Status != protocol.Review || result.State.Version != 6 || len(result.Events) != 3 {
		t.Fatalf("result = %#v", result)
	}
	for _, event := range result.Events {
		if event.Origin != protocol.EventOriginAgent || event.SubmissionID != "sub-001" || event.PolicyDecision != protocol.PolicyDecisionAgentAutoHumanFinal {
			t.Fatalf("event provenance = %#v", event)
		}
	}
	if got := readEvents(t, root, "T-0001"); len(got) != 6 {
		t.Fatalf("event count = %d", len(got))
	}
}

func TestApplyAgentSubmissionRejectsStaleVersionBeforeWritingEvidence(t *testing.T) {
	journal, root := newJournal(t)
	createTask(t, journal, "T-0001")
	_, err := journal.ApplyAgentSubmission(context.Background(), "T-0001", 0, AgentSubmission{
		ID:       "sub-001",
		Actor:    "codex",
		Decision: protocol.PolicyDecisionAgentAutoHumanFinal,
		Action:   protocol.AddEvidence,
		Evidence: []protocol.Evidence{{ID: "E-diff", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: journalTime}},
		At:       journalTime,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "T-0001", "evidence", "E-diff.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale submission wrote evidence: %v", err)
	}
}

func TestApplyAgentSubmissionRejectsHumanFinalApprovalBeforeWriting(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	before, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil {
		t.Fatal(err)
	}
	_, err = journal.ApplyAgentSubmission(context.Background(), "T-0001", before.State.Version, AgentSubmission{
		ID:       "sub-approve",
		Actor:    "codex",
		Decision: protocol.PolicyDecisionAgentAutoHumanFinal,
		Action:   protocol.Approve,
		At:       journalTime,
	})
	if !errors.Is(err, protocol.ErrHumanFinalReviewRequired) {
		t.Fatalf("error = %v", err)
	}
	after, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || after.State != before.State {
		t.Fatalf("after = %#v, %v", after, err)
	}
}

func TestCreateTaskFromAgentRecordsProvenance(t *testing.T) {
	journal, root := newJournal(t)
	if err := journal.CreateTaskFromAgent(context.Background(), testTask("T-0001"), "sub-create", "codex", protocol.PolicyDecisionAgentAutoHumanFinal, journalTime); err != nil {
		t.Fatal(err)
	}
	event := readEvents(t, root, "T-0001")[0]
	if event.Origin != protocol.EventOriginAgent || event.SubmissionID != "sub-create" || event.PolicyDecision != protocol.PolicyDecisionAgentAutoHumanFinal {
		t.Fatalf("event = %#v", event)
	}
}

type failingReplacer struct{}

func (failingReplacer) Replace(string, string) error { return errors.New("replace failed") }

type faultFS struct {
	osFileSystem
	partialTemp, partialNew, partialBackup, failRead bool
}

func (fs *faultFS) ReadFile(path string) ([]byte, error) {
	if fs.failRead {
		return nil, errors.New("read failed")
	}
	return fs.osFileSystem.ReadFile(path)
}

func (fs *faultFS) CreateTemp(dir, pattern string) (SyncFile, string, error) {
	file, path, err := fs.osFileSystem.CreateTemp(dir, pattern)
	return &faultFile{SyncFile: file, partial: fs.partialTemp}, path, err
}
func (fs *faultFS) OpenFile(path string, flag int, perm os.FileMode) (SyncFile, error) {
	file, err := fs.osFileSystem.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	parent := filepath.Base(filepath.Dir(path))
	partial := (fs.partialBackup && parent != "T-0001") || (fs.partialNew && strings.HasPrefix(parent, ".task-"))
	return &faultFile{SyncFile: file, partial: partial}, nil
}
func (fs *faultFS) disableFaults() { *fs = faultFS{} }

type faultFile struct {
	SyncFile
	partial bool
}

type replaceThenBreakReader struct{ fs *faultFS }

func (r replaceThenBreakReader) Replace(tempPath, targetPath string) error {
	if err := os.Rename(tempPath, targetPath); err != nil {
		return err
	}
	r.fs.failRead = true
	return nil
}

func (f *faultFile) Write(data []byte) (int, error) {
	if f.partial {
		return f.SyncFile.Write(data[:len(data)/2])
	}
	return f.SyncFile.Write(data)
}

func newJournal(t *testing.T) (*FileTaskJournal, string) {
	t.Helper()
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Test", CreatedAt: journalTime}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "cc-haha", Name: "CC-HAHA", Capabilities: []string{"execute"}}); err != nil {
		t.Fatal(err)
	}
	return NewFileTaskJournal(root, FlockLocker{}, registry, NewFileEvidenceStore(root)), root
}
func createTask(t *testing.T, journal *FileTaskJournal, taskID string) {
	t.Helper()
	if err := journal.CreateTask(context.Background(), testTask(taskID)); err != nil {
		t.Fatal(err)
	}
}
func testTask(id string) protocol.Task {
	return protocol.Task{ID: id, ProjectID: "project-1", Title: "Test", Objective: "Test", Acceptance: []string{"Pass"}, Creator: "codex", Reviewer: "codex", CreatedAt: journalTime}
}
func writeState(t *testing.T, root string, state protocol.State) {
	t.Helper()
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(root, "tasks", state.TaskID, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func readEvents(t *testing.T, root, id string) []protocol.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "tasks", id, "messages.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []protocol.Event
	for _, line := range bytesLines(data) {
		event, err := protocol.DecodeEventLine(line, id)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}
func writeEvents(t *testing.T, root, id string, events []protocol.Event) {
	t.Helper()
	var data []byte
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		data = append(data, encoded...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", id, "messages.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func bytesLines(data []byte) [][]byte {
	data = data[:len(data)-1]
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	return append(lines, data[start:])
}
