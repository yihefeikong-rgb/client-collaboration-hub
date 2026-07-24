package protocol

import "testing"

func TestTransitionLegalWorkflow(t *testing.T) {
	task := Task{ID: "T-0001", Creator: "codex", Reviewer: "codex"}
	state := State{TaskID: task.ID, Status: Draft}
	steps := []struct {
		action Action
		actor  string
		next   string
		want   Status
	}{
		{Assign, "codex", "cc-haha", Assigned},
		{Accept, "cc-haha", "", Working},
		{Submit, "cc-haha", "", Review},
		{RequestChanges, "codex", "", RevisionRequired},
		{Resume, "cc-haha", "", Working},
		{Submit, "cc-haha", "", Review},
		{Approve, "codex", "", Done},
	}
	for _, step := range steps {
		var err error
		state, err = Transition(state, task, step.actor, step.action, step.next)
		if err != nil {
			t.Fatalf("%s: %v", step.action, err)
		}
		if state.Status != step.want {
			t.Fatalf("%s status = %s, want %s", step.action, state.Status, step.want)
		}
	}
	if state.Version != int64(len(steps)) || state.LastEventID != int64(len(steps)) {
		t.Fatalf("state counters = %+v", state)
	}
}

func TestTransitionRejectsIllegalActions(t *testing.T) {
	task := Task{ID: "T-0001", Creator: "codex", Reviewer: "codex"}
	tests := []struct {
		name   string
		state  State
		actor  string
		action Action
		next   string
	}{
		{"non creator assigns", State{TaskID: task.ID, Status: Draft}, "cc-haha", Assign, "cc-haha"},
		{"empty assignee", State{TaskID: task.ID, Status: Draft}, "codex", Assign, ""},
		{"wrong accept actor", State{TaskID: task.ID, Status: Assigned, ResponsibleClient: "cc-haha"}, "codex", Accept, ""},
		{"non reviewer approves", State{TaskID: task.ID, Status: Review, ResponsibleClient: "cc-haha"}, "cc-haha", Approve, ""},
		{"resume wrong state", State{TaskID: task.ID, Status: Working, ResponsibleClient: "cc-haha"}, "cc-haha", Resume, ""},
		{"block done task", State{TaskID: task.ID, Status: Done}, "codex", Block, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Transition(tt.state, task, tt.actor, tt.action, tt.next); err == nil {
				t.Fatal("Transition() error = nil")
			}
		})
	}
}

func TestTransitionReassignsBlockedTask(t *testing.T) {
	task := Task{ID: "T-0001", Creator: "codex", Reviewer: "codex"}
	state := State{TaskID: task.ID, Status: Blocked, ResponsibleClient: "cc-haha"}
	next, err := Transition(state, task, "codex", Assign, "codex")
	if err != nil || next.Status != Assigned || next.ResponsibleClient != "codex" {
		t.Fatalf("Transition() = %+v, %v", next, err)
	}
}
