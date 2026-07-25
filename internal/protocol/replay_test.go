package protocol

import (
	"fmt"
	"testing"
	"time"
)

type replayEvidenceResolver map[string]Evidence

func (r replayEvidenceResolver) ResolveEvidence(taskID, id string) (Evidence, error) {
	evidence, ok := r[id]
	if !ok || evidence.TaskID != taskID {
		return Evidence{}, fmt.Errorf("evidence %q not found", id)
	}
	return evidence, nil
}

func TestReplayRejectsIllegalIntermediateTransition(t *testing.T) {
	task := replayTask()
	events := []Event{
		{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
		{EventID: 2, TaskID: task.ID, Type: EventApproved, Actor: "codex", At: task.CreatedAt, ExpectedVersion: 1},
	}
	if _, err := Replay(task, events, testReferences{}, replayEvidenceResolver{}); err == nil {
		t.Fatal("task_created -> approved accepted")
	}
}

func TestReplayEvidenceAddedPreservesBusinessState(t *testing.T) {
	task := replayTask()
	evidence := Evidence{ID: "E-0001", TaskID: task.ID, Kind: EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: task.CreatedAt}
	events := []Event{
		{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
		{EventID: 2, TaskID: task.ID, Type: EventEvidenceAdded, Actor: "codex", At: task.CreatedAt, Body: evidence.Summary, EvidenceRefs: []string{evidence.ID}, ExpectedVersion: 1},
	}
	state, err := Replay(task, events, testReferences{}, replayEvidenceResolver{evidence.ID: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != Draft || state.Version != 2 || state.LastEventID != 2 || state.ResponsibleClient != task.Creator {
		t.Fatalf("state = %+v", state)
	}
}

func TestReplayRejectsUnregisteredOrNonExecutingAssignmentTarget(t *testing.T) {
	task := replayTask()
	for _, target := range []string{"ghost-client", "codex"} {
		t.Run(target, func(t *testing.T) {
			events := []Event{
				{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
				{EventID: 2, TaskID: task.ID, Type: EventAssigned, Actor: "codex", At: task.CreatedAt, TargetClient: target, ExpectedVersion: 1},
			}
			if _, err := Replay(task, events, testReferences{}, replayEvidenceResolver{}); err == nil {
				t.Fatalf("target %q accepted", target)
			}
		})
	}
}

func TestReplayRequiresEvidenceToBeAnnouncedBeforeUse(t *testing.T) {
	task := replayTask()
	diff := Evidence{ID: "E-diff", TaskID: task.ID, Kind: EvidenceDiff, Summary: "Diff", CreatedBy: "cc-haha", CreatedAt: task.CreatedAt}
	testEvidence := Evidence{ID: "E-test", TaskID: task.ID, Kind: EvidenceTest, Summary: "Tests", CreatedBy: "cc-haha", CreatedAt: task.CreatedAt}
	events := []Event{
		{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
		{EventID: 2, TaskID: task.ID, Type: EventAssigned, Actor: "codex", At: task.CreatedAt, TargetClient: "cc-haha", ExpectedVersion: 1},
		{EventID: 3, TaskID: task.ID, Type: EventAccepted, Actor: "cc-haha", At: task.CreatedAt, ExpectedVersion: 2},
		{EventID: 4, TaskID: task.ID, Type: EventSubmitted, Actor: "cc-haha", At: task.CreatedAt, EvidenceRefs: []string{diff.ID, testEvidence.ID}, ExpectedVersion: 3},
	}
	if _, err := Replay(task, events, testReferences{}, replayEvidenceResolver{diff.ID: diff, testEvidence.ID: testEvidence}); err == nil {
		t.Fatal("unannounced evidence was accepted")
	}
}

func TestReplayRejectsEvidenceAnnouncedAfterSubmission(t *testing.T) {
	task := replayTask()
	diff := Evidence{ID: "E-diff", TaskID: task.ID, Kind: EvidenceDiff, Summary: "Diff", CreatedBy: "cc-haha", CreatedAt: task.CreatedAt}
	testEvidence := Evidence{ID: "E-test", TaskID: task.ID, Kind: EvidenceTest, Summary: "Tests", CreatedBy: "cc-haha", CreatedAt: task.CreatedAt}
	events := []Event{
		{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
		{EventID: 2, TaskID: task.ID, Type: EventAssigned, Actor: "codex", At: task.CreatedAt, TargetClient: "cc-haha", ExpectedVersion: 1},
		{EventID: 3, TaskID: task.ID, Type: EventAccepted, Actor: "cc-haha", At: task.CreatedAt, ExpectedVersion: 2},
		{EventID: 4, TaskID: task.ID, Type: EventSubmitted, Actor: "cc-haha", At: task.CreatedAt, EvidenceRefs: []string{diff.ID, testEvidence.ID}, ExpectedVersion: 3},
		{EventID: 5, TaskID: task.ID, Type: EventEvidenceAdded, Actor: "cc-haha", At: task.CreatedAt, Body: diff.Summary, EvidenceRefs: []string{diff.ID}, ExpectedVersion: 4},
		{EventID: 6, TaskID: task.ID, Type: EventEvidenceAdded, Actor: "cc-haha", At: task.CreatedAt, Body: testEvidence.Summary, EvidenceRefs: []string{testEvidence.ID}, ExpectedVersion: 5},
	}
	if _, err := Replay(task, events, testReferences{}, replayEvidenceResolver{diff.ID: diff, testEvidence.ID: testEvidence}); err == nil {
		t.Fatal("submission accepted evidence announced in the future")
	}
}

func TestReplayRejectsDuplicateEvidenceAnnouncement(t *testing.T) {
	task := replayTask()
	evidence := Evidence{ID: "E-0001", TaskID: task.ID, Kind: EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: task.CreatedAt}
	events := []Event{
		{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
		{EventID: 2, TaskID: task.ID, Type: EventEvidenceAdded, Actor: "codex", At: task.CreatedAt, Body: evidence.Summary, EvidenceRefs: []string{evidence.ID}, ExpectedVersion: 1},
		{EventID: 3, TaskID: task.ID, Type: EventEvidenceAdded, Actor: "codex", At: task.CreatedAt, Body: evidence.Summary, EvidenceRefs: []string{evidence.ID}, ExpectedVersion: 2},
	}
	if _, err := Replay(task, events, testReferences{}, replayEvidenceResolver{evidence.ID: evidence}); err == nil {
		t.Fatal("duplicate evidence announcement was accepted")
	}
}

func replayTask() Task {
	return Task{ID: "T-0001", ProjectID: "project-1", Title: "Test", Objective: "Test", Acceptance: []string{"Pass"}, Creator: "codex", Reviewer: "codex", CreatedAt: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)}
}
