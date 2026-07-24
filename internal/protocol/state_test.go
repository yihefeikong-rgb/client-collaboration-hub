package protocol

import (
	"testing"
	"time"
)

var transitionTime = time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

func TestTransitionTransfersResponsibility(t *testing.T) {
	task := Task{ID: "T-0001", Creator: "codex", Reviewer: "codex"}
	state := State{TaskID: task.ID, Status: Draft, ResponsibleClient: "codex", UpdatedAt: transitionTime}
	steps := []struct {
		request TransitionRequest
		status  Status
		owner   string
	}{
		{TransitionRequest{Action: Assign, Actor: "codex", NextAssignee: "cc-haha", At: transitionTime}, Assigned, "cc-haha"},
		{TransitionRequest{Action: Accept, Actor: "cc-haha", At: transitionTime}, Working, "cc-haha"},
		{TransitionRequest{Action: Submit, Actor: "cc-haha", EvidenceKinds: []EvidenceKind{EvidenceDiff, EvidenceTest}, At: transitionTime}, Review, "codex"},
		{TransitionRequest{Action: RequestChanges, Actor: "codex", Feedback: "fix test", At: transitionTime}, RevisionRequired, "cc-haha"},
		{TransitionRequest{Action: Resume, Actor: "cc-haha", At: transitionTime}, Working, "cc-haha"},
		{TransitionRequest{Action: Submit, Actor: "cc-haha", EvidenceKinds: []EvidenceKind{EvidenceArtifact, EvidenceTest}, At: transitionTime}, Review, "codex"},
		{TransitionRequest{Action: Approve, Actor: "codex", At: transitionTime}, Done, "codex"},
	}
	for _, step := range steps {
		var err error
		state, err = Transition(state, task, step.request)
		if err != nil {
			t.Fatalf("%s: %v", step.request.Action, err)
		}
		if state.Status != step.status || state.ResponsibleClient != step.owner || state.AssignedClient != "cc-haha" {
			t.Fatalf("%s state = %+v", step.request.Action, state)
		}
		if !state.UpdatedAt.Equal(transitionTime) {
			t.Fatalf("transition used an unexpected time: %s", state.UpdatedAt)
		}
	}
}

func TestTransitionRejectsMissingBusinessPreconditions(t *testing.T) {
	task := Task{ID: "T-0001", Creator: "codex", Reviewer: "codex"}
	tests := []struct {
		name    string
		state   State
		request TransitionRequest
	}{
		{"submit missing test", activeState(task, Working), TransitionRequest{Action: Submit, Actor: "cc-haha", EvidenceKinds: []EvidenceKind{EvidenceDiff}, At: transitionTime}},
		{"submit missing result", activeState(task, Working), TransitionRequest{Action: Submit, Actor: "cc-haha", EvidenceKinds: []EvidenceKind{EvidenceTest}, At: transitionTime}},
		{"empty feedback", activeState(task, Review), TransitionRequest{Action: RequestChanges, Actor: "codex", Feedback: " \t", At: transitionTime}},
		{"block missing blocker", activeState(task, Working), TransitionRequest{Action: Block, Actor: "cc-haha", At: transitionTime}},
		{"invalid actor", activeState(task, Working), TransitionRequest{Action: Submit, Actor: "../bad", EvidenceKinds: []EvidenceKind{EvidenceDiff, EvidenceTest}, At: transitionTime}},
		{"non UTC time", activeState(task, Working), TransitionRequest{Action: Submit, Actor: "cc-haha", EvidenceKinds: []EvidenceKind{EvidenceDiff, EvidenceTest}, At: transitionTime.In(time.FixedZone("CST", 8*3600))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Transition(tt.state, task, tt.request); err == nil {
				t.Fatal("Transition() error = nil")
			}
		})
	}
}

func TestStateValidateCombinations(t *testing.T) {
	task := Task{ID: "T-0001", Creator: "codex", Reviewer: "codex"}
	valid := []State{
		{TaskID: task.ID, Status: Draft, ResponsibleClient: "codex", UpdatedAt: transitionTime},
		activeState(task, Assigned), activeState(task, Working), activeState(task, Review),
		activeState(task, RevisionRequired), activeState(task, Done), activeState(task, Blocked),
	}
	for _, state := range valid {
		if err := state.Validate(task); err != nil {
			t.Fatalf("valid state %+v: %v", state, err)
		}
	}
	invalid := activeState(task, Review)
	invalid.ResponsibleClient = "cc-haha"
	if err := invalid.Validate(task); err == nil {
		t.Fatal("invalid REVIEW state accepted")
	}
	for _, state := range []State{
		{TaskID: task.ID, Status: Draft, ResponsibleClient: "cc-haha", UpdatedAt: transitionTime},
		{TaskID: task.ID, Status: Assigned, AssignedClient: "cc-haha", ResponsibleClient: "codex", UpdatedAt: transitionTime},
		{TaskID: task.ID, Status: Working, AssignedClient: "", ResponsibleClient: "", UpdatedAt: transitionTime},
		{TaskID: task.ID, Status: RevisionRequired, AssignedClient: "cc-haha", ResponsibleClient: "codex", UpdatedAt: transitionTime},
		{TaskID: task.ID, Status: Done, AssignedClient: "cc-haha", ResponsibleClient: "cc-haha", UpdatedAt: transitionTime},
		{TaskID: task.ID, Status: Blocked, AssignedClient: "cc-haha", ResponsibleClient: "other", UpdatedAt: transitionTime},
	} {
		if err := state.Validate(task); err == nil {
			t.Fatalf("invalid state accepted: %+v", state)
		}
	}
}

func TestTransitionReassignsBlockedTask(t *testing.T) {
	task := Task{ID: "T-0001", Creator: "codex", Reviewer: "codex"}
	state := activeState(task, Blocked)
	next, err := Transition(state, task, TransitionRequest{Action: Assign, Actor: "codex", NextAssignee: "reviewer", At: transitionTime})
	if err != nil || next.Status != Assigned || next.AssignedClient != "reviewer" || next.ResponsibleClient != "reviewer" {
		t.Fatalf("Transition() = %+v, %v", next, err)
	}
}

func activeState(task Task, status Status) State {
	responsible := "cc-haha"
	if status == Review || status == Done {
		responsible = task.Reviewer
	}
	return State{TaskID: task.ID, Status: status, AssignedClient: "cc-haha", ResponsibleClient: responsible, UpdatedAt: transitionTime}
}
