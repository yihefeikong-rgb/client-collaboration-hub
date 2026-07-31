package webconsole

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectPolicyFieldsSerialize(t *testing.T) {
	project := Project{
		ID:            "demo",
		Name:          "Demo",
		CreatedAt:     "2026-07-31T09:00:00Z",
		FinalReview:   "agent",
		AutoDone:      true,
		PolicyVersion: 2,
		RecentPolicyAudit: &PolicyAuditView{
			Version:  2,
			Actor:    "codex",
			At:       "2026-07-31T10:00:00Z",
			Previous: PolicyView{FinalReview: "human", AutoDone: false},
			Current:  PolicyView{FinalReview: "agent", AutoDone: true},
		},
	}
	data, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, field := range []string{`"final_review":"agent"`, `"auto_done":true`, `"policy_version":2`, `"recent_policy_audit"`} {
		if !strings.Contains(text, field) {
			t.Fatalf("serialized project %s lacks %s", text, field)
		}
	}
}

func TestProjectPolicyFieldsOmitWhenEmpty(t *testing.T) {
	data, err := json.Marshal(Project{ID: "demo", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, field := range []string{`"final_review"`, `"auto_done"`, `"policy_version"`, `"recent_policy_audit"`} {
		if strings.Contains(text, field) {
			t.Fatalf("serialized empty policy project %s leaks %s", text, field)
		}
	}
}
