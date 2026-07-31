package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
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

func TestMessageWakeRuleFor(t *testing.T) {
	cases := []struct {
		status      protocol.Status
		responsible string
		wantClient  string
	}{
		{protocol.Assigned, "cc-haha", "cc-haha"},
		{protocol.Working, "cc-haha", "cc-haha"},
		{protocol.RevisionRequired, "cc-haha", "cc-haha"},
		{protocol.Review, "codex", "codex"},
		{protocol.Done, "cc-haha", ""},
		{protocol.Blocked, "cc-haha", ""},
		{protocol.Draft, "codex", ""},
	}
	for _, tc := range cases {
		rule := messageWakeRuleFor(tc.status, tc.responsible)
		if tc.wantClient == "" && rule != nil {
			t.Fatalf("messageWakeRuleFor(%s, %s) = %+v, want nil", tc.status, tc.responsible, rule)
		}
		if tc.wantClient != "" && (rule == nil || rule.Client != tc.wantClient) {
			t.Fatalf("messageWakeRuleFor(%s, %s) = %+v, want client %s", tc.status, tc.responsible, rule, tc.wantClient)
		}
	}
}

func TestCCSessionUUIDStable(t *testing.T) {
	first := ccSessionUUID("PILOT-WAKE-001")
	second := ccSessionUUID("PILOT-WAKE-001")
	if first != second {
		t.Fatalf("ccSessionUUID is not deterministic: %s != %s", first, second)
	}
	other := ccSessionUUID("PILOT-WAKE-002")
	if first == other {
		t.Fatalf("ccSessionUUID collides for different tasks: %s", first)
	}
	if len(first) != 36 {
		t.Fatalf("ccSessionUUID = %q, want 36-char UUID", first)
	}
	if first[14] != '5' {
		t.Fatalf("ccSessionUUID %q is not UUIDv5 (version nibble at position 14)", first)
	}
	if !(first[19] == '8' || first[19] == '9' || first[19] == 'a' || first[19] == 'b') {
		t.Fatalf("ccSessionUUID %q has invalid variant nibble", first)
	}
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for index := 0; index < 200; index++ {
		value := ccSessionUUID(fmt.Sprintf("TASK-%04d", index))
		if !uuidPattern.MatchString(value) {
			t.Fatalf("ccSessionUUID produced invalid UUID %q for task %d", value, index)
		}
	}
}

func TestWakeRuleAndPromptForSupplementMessage(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-WAKE-MSG")
	messageTime := app.now().Add(time.Second)
	if _, err := app.Journal.AppendMessage(context.Background(), "T-WAKE-MSG", 2, "codex", "请先补上测试再提交", messageTime); err != nil {
		t.Fatal(err)
	}

	snapshot, err := app.Query.Snapshot(context.Background(), "T-WAKE-MSG", 0)
	if err != nil {
		t.Fatal(err)
	}
	rule, prompt := wakeRuleAndPrompt(snapshot)
	if rule == nil || rule.Client != "cc-haha" {
		t.Fatalf("wakeRuleAndPrompt = %+v, want cc-haha message wake", rule)
	}
	if !strings.Contains(prompt, "补充消息：请先补上测试再提交") {
		t.Fatalf("prompt does not carry supplement message: %s", prompt)
	}
	if !strings.Contains(prompt, "任务：T-WAKE-MSG") {
		t.Fatalf("prompt does not carry task id: %s", prompt)
	}
}

func TestWakeRuleAndPromptIgnoresOwnMessage(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-WAKE-SELF")
	messageTime := app.now().Add(time.Second)
	if _, err := app.Journal.AppendMessage(context.Background(), "T-WAKE-SELF", 2, "cc-haha", "我开始了", messageTime); err != nil {
		t.Fatal(err)
	}

	snapshot, err := app.Query.Snapshot(context.Background(), "T-WAKE-SELF", 0)
	if err != nil {
		t.Fatal(err)
	}
	rule, prompt := wakeRuleAndPrompt(snapshot)
	if rule == nil {
		t.Fatal("own message should not suppress the state wake rule")
	}
	if strings.Contains(prompt, "补充消息") {
		t.Fatalf("own message must not be treated as supplement: %s", prompt)
	}
}

func TestWakeRuleAndPromptForWorkingStateMessage(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-WAKE-WORK")
	acceptTime := app.now().Add(time.Second)
	intent := protocol.TransitionIntent{
		Action: protocol.Accept, Actor: "cc-haha", At: acceptTime,
		Origin: protocol.EventOriginHuman, PolicyDecision: protocol.PolicyDecisionHumanOperator,
	}
	if _, err := app.Journal.CommitTransition(context.Background(), "T-WAKE-WORK", 2, intent, nil); err != nil {
		t.Fatal(err)
	}
	messageTime := acceptTime.Add(time.Second)
	if _, err := app.Journal.AppendMessage(context.Background(), "T-WAKE-WORK", 3, "codex", "注意目录权限", messageTime); err != nil {
		t.Fatal(err)
	}

	snapshot, err := app.Query.Snapshot(context.Background(), "T-WAKE-WORK", 0)
	if err != nil {
		t.Fatal(err)
	}
	rule, prompt := wakeRuleAndPrompt(snapshot)
	if rule == nil || rule.Client != "cc-haha" {
		t.Fatalf("wakeRuleAndPrompt = %+v, want cc-haha message wake while working", rule)
	}
	if !strings.Contains(prompt, "补充消息：注意目录权限") {
		t.Fatalf("prompt does not carry supplement message: %s", prompt)
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
