package protocol

import (
	"reflect"
	"testing"
	"time"
)

type policyReferences struct {
	capabilities map[string]map[string]bool
}

func (r policyReferences) ProjectExists(string) bool { return true }
func (r policyReferences) ClientExists(id string) bool {
	_, ok := r.capabilities[id]
	return ok
}
func (r policyReferences) ClientHasCapability(id, capability string) bool {
	return r.capabilities[id][capability]
}

func TestDefaultActionPolicyTableAndAuthorizationBijection(t *testing.T) {
	refs := policyReferences{capabilities: map[string]map[string]bool{
		"creator":  {"create_task": true},
		"reviewer": {"review": true},
		"executor": {"execute": true},
		"other":    {"create_task": true, "review": true, "execute": true},
	}}
	task := Task{ID: "T-0001", ProjectID: "project-1", Title: "Task", Objective: "Objective", Acceptance: []string{"Pass"}, Creator: "creator", Reviewer: "reviewer", CreatedAt: policyTime}
	policy := DefaultActionPolicy{}
	tests := []struct {
		status Status
		actor  string
		want   []Action
	}{
		{Draft, "creator", []Action{Assign, Message, AddEvidence}},
		{Draft, "reviewer", []Action{Message, AddEvidence}},
		{Draft, "executor", nil},
		{Draft, "other", nil},
		{Assigned, "creator", []Action{Block, Message, AddEvidence}},
		{Assigned, "reviewer", []Action{Message, AddEvidence}},
		{Assigned, "executor", []Action{Accept, Block, Message, AddEvidence}},
		{Assigned, "other", nil},
		{Working, "creator", []Action{Block, Message, AddEvidence}},
		{Working, "reviewer", []Action{Message, AddEvidence}},
		{Working, "executor", []Action{Submit, Block, Message, AddEvidence}},
		{Working, "other", nil},
		{Review, "creator", []Action{Block, Message, AddEvidence}},
		{Review, "reviewer", []Action{RequestChanges, Approve, Block, Message, AddEvidence}},
		{Review, "executor", []Action{Message, AddEvidence}},
		{Review, "other", nil},
		{RevisionRequired, "creator", []Action{Message, AddEvidence}},
		{RevisionRequired, "reviewer", []Action{Message, AddEvidence}},
		{RevisionRequired, "executor", []Action{Resume, Message, AddEvidence}},
		{RevisionRequired, "other", nil},
		{Blocked, "creator", []Action{Assign, Message, AddEvidence}},
		{Blocked, "reviewer", []Action{Message, AddEvidence}},
		{Blocked, "executor", []Action{Message, AddEvidence}},
		{Blocked, "other", nil},
		{Done, "creator", nil},
		{Done, "reviewer", nil},
		{Done, "executor", nil},
		{Done, "other", nil},
	}
	for _, test := range tests {
		t.Run(string(test.status)+"/"+test.actor, func(t *testing.T) {
			state := policyState(task, test.status)
			got := policy.AllowedActions(task, state, test.actor, refs)
			if len(got) != len(test.want) || (len(got) > 0 && !reflect.DeepEqual(got, test.want)) {
				t.Fatalf("AllowedActions() = %v, want %v", got, test.want)
			}
			for _, action := range []Action{Assign, Accept, Submit, RequestChanges, Resume, Approve, Block, Message, AddEvidence} {
				allowed := containsPolicyAction(got, action)
				err := policy.Authorize(task, state, test.actor, action, refs)
				if allowed != (err == nil) {
					t.Fatalf("action %s allowed=%t authorize=%v", action, allowed, err)
				}
			}
		})
	}
}

var policyTime = time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

func policyState(task Task, status Status) State {
	state := State{TaskID: task.ID, Status: status, Version: 1, LastEventID: 1, UpdatedAt: policyTime}
	switch status {
	case Draft:
		state.ResponsibleClient = task.Creator
	case Assigned, Working, RevisionRequired, Blocked:
		state.AssignedClient = "executor"
		state.ResponsibleClient = "executor"
	case Review, Done:
		state.AssignedClient = "executor"
		state.ResponsibleClient = task.Reviewer
	}
	return state
}

func containsPolicyAction(actions []Action, wanted Action) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}
