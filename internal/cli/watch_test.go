package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

func TestWakeRuleFor(t *testing.T) {
	cases := []struct {
		status      protocol.Status
		responsible string
		wantClient  string
	}{
		{protocol.Assigned, "cc-haha", "cc-haha"},
		{protocol.RevisionRequired, "cc-haha", "cc-haha"},
		{protocol.Review, "codex", "codex"},
		{protocol.Assigned, "codex", ""},
		{protocol.Review, "cc-haha", ""},
		{protocol.Working, "cc-haha", ""},
		{protocol.Done, "codex", ""},
		{protocol.Blocked, "cc-haha", ""},
	}
	for _, tc := range cases {
		rule := wakeRuleFor(tc.status, tc.responsible)
		if tc.wantClient == "" && rule != nil {
			t.Fatalf("wakeRuleFor(%s, %s) = %+v, want nil", tc.status, tc.responsible, rule)
		}
		if tc.wantClient != "" && (rule == nil || rule.Client != tc.wantClient) {
			t.Fatalf("wakeRuleFor(%s, %s) = %+v, want client %s", tc.status, tc.responsible, rule, tc.wantClient)
		}
	}
}

func TestWakeNotifierDryRunMarksOnce(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-WAKE-1")

	notifier := &wakeNotifier{
		app:       app,
		interval:  time.Second,
		dryRun:    true,
		statePath: filepath.Join(t.TempDir(), wakeStateFileName),
		notified:  map[string]bool{},
		running:   map[string]bool{},
	}
	notifier.scan(context.Background())

	snapshot, err := app.Query.Snapshot(context.Background(), "T-WAKE-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	key := wakeKey(snapshot)
	notifier.mu.Lock()
	notified := notifier.notified[key]
	running := len(notifier.running)
	notifier.mu.Unlock()
	if !notified {
		t.Fatalf("dry-run scan did not mark key %q", key)
	}
	if running != 0 {
		t.Fatalf("dry-run scan left running clients: %d", running)
	}

	notifier.scan(context.Background())
	notifier.mu.Lock()
	count := len(notifier.notified)
	notifier.mu.Unlock()
	if count != 1 {
		t.Fatalf("second scan recorded %d keys, want 1", count)
	}
}

func TestWakeNotifierBusyClientIsNotMarked(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-WAKE-2")

	notifier := &wakeNotifier{
		app:       app,
		interval:  time.Second,
		dryRun:    true,
		statePath: filepath.Join(t.TempDir(), wakeStateFileName),
		notified:  map[string]bool{},
		running:   map[string]bool{"cc-haha": true},
	}
	notifier.scan(context.Background())

	snapshot, err := app.Query.Snapshot(context.Background(), "T-WAKE-2", 0)
	if err != nil {
		t.Fatal(err)
	}
	key := wakeKey(snapshot)
	notifier.mu.Lock()
	notified := notifier.notified[key]
	notifier.mu.Unlock()
	if notified {
		t.Fatalf("busy client task was marked as notified")
	}
}

func assignTask(t *testing.T, app *App, taskID string) {
	t.Helper()
	task := protocol.Task{
		ID: taskID, ProjectID: "project-1", Title: "Wake task", Objective: "Verify watcher",
		Acceptance: []string{"Watcher wakes the client"}, Creator: "codex", Reviewer: "codex", CreatedAt: app.now(),
	}
	if err := app.Journal.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	intent := protocol.TransitionIntent{
		Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: app.now(),
		Origin: protocol.EventOriginHuman, PolicyDecision: protocol.PolicyDecisionHumanOperator,
	}
	if _, err := app.Journal.CommitTransition(context.Background(), taskID, 1, intent, nil); err != nil {
		t.Fatal(err)
	}
}

func wakeKey(snapshot store.TaskSnapshot) string {
	return fmt.Sprintf("%s|%s|%d", snapshot.Task.ID, snapshot.State.Status, snapshot.State.Version)
}
