package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

func TestProjectPolicySwitchesFinalReviewMode(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	projectPath := t.TempDir()
	result, err := app.RegisterLocalProject(context.Background(), "policy-project", "Policy", projectPath)
	if err != nil {
		t.Fatal(err)
	}

	if code := app.Run([]string{"project", "policy", "--project", result.ProjectID, "--final-review", "agent", "--actor", "operator"}); code != ExitOK {
		t.Fatalf("switch to agent: code=%d", code)
	}
	project, err := app.Registry.ReadProject(context.Background(), result.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.CollaborationPolicy.FinalReview != protocol.FinalReviewAgent || !project.CollaborationPolicy.AutoDone {
		t.Fatalf("policy after agent switch = %+v", project.CollaborationPolicy)
	}
	if project.PolicyVersion != 2 || len(project.PolicyHistory) != 1 {
		t.Fatalf("policy version=%d history=%d, want 2/1", project.PolicyVersion, len(project.PolicyHistory))
	}
	entry := project.PolicyHistory[0]
	if entry.Actor != "operator" || entry.Origin != protocol.EventOriginHuman || entry.Previous.FinalReview != protocol.FinalReviewHuman || entry.Current.FinalReview != protocol.FinalReviewAgent {
		t.Fatalf("unexpected audit entry: %+v", entry)
	}

	if code := app.Run([]string{"project", "policy", "--project", result.ProjectID, "--final-review", "human"}); code != ExitOK {
		t.Fatalf("switch back to human: code=%d", code)
	}
	project, err = app.Registry.ReadProject(context.Background(), result.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.CollaborationPolicy.FinalReview != protocol.FinalReviewHuman || project.CollaborationPolicy.AutoDone {
		t.Fatalf("policy after human switch = %+v", project.CollaborationPolicy)
	}
	if project.PolicyVersion != 3 || len(project.PolicyHistory) != 2 {
		t.Fatalf("policy version=%d history=%d, want 3/2", project.PolicyVersion, len(project.PolicyHistory))
	}
}

func TestProjectPolicyUnchangedDoesNotWriteHistory(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := app.RegisterLocalProject(context.Background(), "policy-project", "Policy", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code := app.Run([]string{"project", "policy", "--project", result.ProjectID, "--final-review", "human"}); code != ExitOK {
			t.Fatalf("unchanged switch: code=%d", code)
		}
	}
	project, err := app.Registry.ReadProject(context.Background(), result.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.PolicyVersion != 1 || len(project.PolicyHistory) != 0 {
		t.Fatalf("unchanged switch wrote history: version=%d history=%d", project.PolicyVersion, len(project.PolicyHistory))
	}
}

func TestProjectPolicyRejectsInvalidInput(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := app.RegisterLocalProject(context.Background(), "policy-project", "Policy", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if code := app.Run([]string{"project", "policy", "--project", result.ProjectID, "--final-review", "nonsense"}); code != ExitValidation {
		t.Fatalf("invalid review mode: code=%d", code)
	}
	if code := app.Run([]string{"project", "policy", "--project", result.ProjectID}); code != ExitValidation {
		t.Fatalf("missing review mode: code=%d", code)
	}
	if code := app.Run([]string{"project", "policy", "--project", "missing-project", "--final-review", "agent"}); code == ExitOK {
		t.Fatal("unknown project was accepted")
	}
}
