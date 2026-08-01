package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
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
		{protocol.Review, "reasonix", "reasonix"},
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

func TestResolveClientDefaultPrecedence(t *testing.T) {
	if got := resolveClientDefault("default", true, "delivery"); got != "default" {
		t.Fatalf("explicit flag precedence = %q", got)
	}
	if got := resolveClientDefault("default", false, "delivery"); got != "delivery" {
		t.Fatalf("registry default precedence = %q", got)
	}
	if got := resolveClientDefault("default", false, ""); got != "default" {
		t.Fatalf("builtin fallback = %q", got)
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
		{protocol.Review, "reasonix", "reasonix"},
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
	if !strings.Contains(prompt, "任务进展摘要") || !strings.Contains(prompt, "当前状态") || !strings.Contains(prompt, "补充消息：请先补上测试再提交") {
		t.Fatalf("prompt does not carry progress summary: %s", prompt)
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

func TestWakeNotifierKeepsFreshDesktopWakeUntilStallTimeout(t *testing.T) {
	output := &bytes.Buffer{}
	app := NewApp(t.TempDir(), output, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-WAKE-STALLED")
	snapshot, err := app.Query.Snapshot(context.Background(), "T-WAKE-STALLED", 0)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("%s|%s|%d", snapshot.Task.ID, snapshot.State.Status, snapshot.State.Version)
	notifier := &wakeNotifier{
		app:          app,
		dryRun:       true,
		stallTimeout: time.Minute,
		statePath:    filepath.Join(t.TempDir(), wakeStateFileName),
		notified:     map[string]bool{key: true},
		wakeAt:       map[string]time.Time{key: app.Clock()},
		running:      map[string]bool{},
		retryAfter:   map[string]time.Time{},
	}
	notifier.scan(context.Background())
	if strings.Contains(output.String(), "WOULD wake") {
		t.Fatalf("fresh desktop wake was retried: %s", output.String())
	}

	notifier.wakeAt[key] = app.Clock().Add(-time.Minute)
	notifier.scan(context.Background())
	if !strings.Contains(output.String(), "WOULD wake cc-haha") {
		t.Fatalf("stalled desktop wake was not retried: %s", output.String())
	}
}

func TestWakeNotifierDoesNotReplayUncertainDesktopDelivery(t *testing.T) {
	output := &bytes.Buffer{}
	app := NewApp(t.TempDir(), output, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "TASK-UNCERTAIN-42")
	snapshot, err := app.Query.Snapshot(context.Background(), "TASK-UNCERTAIN-42", 0)
	if err != nil {
		t.Fatal(err)
	}
	key := wakeKey(snapshot)
	notifier := &wakeNotifier{
		app:          app,
		dryRun:       true,
		stallTimeout: time.Nanosecond,
		statePath:    filepath.Join(t.TempDir(), wakeStateFileName),
		notified:     map[string]bool{key: true},
		wakeAt:       map[string]time.Time{key: app.Clock().Add(-time.Hour)},
		running:      map[string]bool{},
		retryAfter:   map[string]time.Time{},
		deliveries: map[string]wakeDelivery{
			key: {
				ID:        wakeDeliveryID(key, "reasonix"),
				TaskID:    snapshot.Task.ID,
				Client:    "reasonix",
				Status:    wakeDeliveryUncertain,
				UpdatedAt: app.Clock(),
			},
		},
	}

	notifier.scan(context.Background())

	if strings.Contains(output.String(), "WOULD wake") {
		t.Fatalf("uncertain desktop delivery was replayed: %s", output.String())
	}
	if got := notifier.deliveries[key].Status; got != wakeDeliveryUncertain {
		t.Fatalf("delivery status = %q, want %q", got, wakeDeliveryUncertain)
	}
}

func TestCCHahaDeliveryAckRequiresMatchingExplicitAcceptance(t *testing.T) {
	if err := validateCCHahaDeliveryAck("delivery-42", "delivery-42", "accepted"); err != nil {
		t.Fatalf("matching accepted acknowledgement = %v", err)
	}
	for _, tc := range []struct {
		name           string
		acknowledgedID string
		state          string
	}{
		{name: "different delivery", acknowledgedID: "delivery-other", state: "accepted"},
		{name: "legacy acknowledgement has no id", state: "accepted"},
		{name: "nonaccepted state", acknowledgedID: "delivery-42", state: "queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateCCHahaDeliveryAck("delivery-42", tc.acknowledgedID, tc.state); !isUncertainDelivery(err) {
				t.Fatalf("acknowledgement error = %v, want uncertain delivery", err)
			}
		})
	}
}

func TestUnsupportedCCHahaDesktopDeliveryIsUncertain(t *testing.T) {
	if err := unsupportedCCHahaDesktopDelivery(); !isUncertainDelivery(err) {
		t.Fatalf("unsupported fallback error = %v, want uncertain delivery", err)
	}
}

func TestWatchDeliveryResolutionIsHumanAudited(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	app := NewApp(root, &stdout, &stderr, func() time.Time { return now })
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "TASK-RESOLVE-42")
	snapshot, err := app.Query.Snapshot(context.Background(), "TASK-RESOLVE-42", 0)
	if err != nil {
		t.Fatal(err)
	}
	key := wakeKey(snapshot)
	deliveryID := wakeDeliveryID(key, "reasonix")
	notifier := &wakeNotifier{
		app:        app,
		statePath:  filepath.Join(root, "collaboration", ".runtime", wakeStateFileName),
		notified:   map[string]bool{key: true},
		wakeAt:     map[string]time.Time{key: now},
		running:    map[string]bool{},
		retryAfter: map[string]time.Time{},
		deliveries: map[string]wakeDelivery{
			key: {ID: deliveryID, TaskID: snapshot.Task.ID, Client: "reasonix", Status: wakeDeliveryUncertain, UpdatedAt: now},
		},
	}
	if err := notifier.save(); err != nil {
		t.Fatal(err)
	}

	if code := app.Run([]string{"--json", "watch", "delivery", "resolve", "--delivery", deliveryID, "--actor", "operator", "--note", "已在 RE 可见对话中核对"}); code != ExitOK {
		t.Fatalf("resolve code = %d stderr=%s", code, stderr.String())
	}
	var resolved wakeDeliveryResolutionOutput
	if err := json.Unmarshal(stdout.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Resolution != wakeDeliveryResolutionResolved || resolved.RetryAllowed {
		t.Fatalf("resolve output = %+v", resolved)
	}

	loaded := &wakeNotifier{app: app, statePath: notifier.statePath}
	if err := loaded.load(); err != nil {
		t.Fatal(err)
	}
	if got := loaded.deliveries[key].Status; got != wakeDeliveryResolved {
		t.Fatalf("resolved delivery status = %q, want %q", got, wakeDeliveryResolved)
	}
	if len(loaded.deliveryAudit) != 1 || loaded.deliveryAudit[0].Actor != "operator" || loaded.deliveryAudit[0].Resolution != wakeDeliveryResolutionResolved {
		t.Fatalf("resolve audit = %+v", loaded.deliveryAudit)
	}

	abandonedKey := key + "|retry"
	abandonedID := wakeDeliveryID(abandonedKey, "reasonix")
	loaded.mu.Lock()
	loaded.ensureStateMapsLocked()
	loaded.deliveries[abandonedKey] = wakeDelivery{ID: abandonedID, TaskID: snapshot.Task.ID, Client: "reasonix", Status: wakeDeliveryUncertain, UpdatedAt: now}
	loaded.notified[abandonedKey] = true
	loaded.wakeAt[abandonedKey] = now
	loaded.mu.Unlock()
	if err := loaded.save(); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := app.Run([]string{"--json", "watch", "delivery", "abandon", "--delivery", abandonedID, "--actor", "operator", "--note", "已确认未投递，允许重新发送"}); code != ExitOK {
		t.Fatalf("abandon code = %d stderr=%s", code, stderr.String())
	}
	var abandoned wakeDeliveryResolutionOutput
	if err := json.Unmarshal(stdout.Bytes(), &abandoned); err != nil {
		t.Fatal(err)
	}
	if abandoned.Resolution != wakeDeliveryResolutionAbandoned || !abandoned.RetryAllowed {
		t.Fatalf("abandon output = %+v", abandoned)
	}
	if err := loaded.load(); err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded.deliveries[abandonedKey]; exists {
		t.Fatal("abandoned delivery remained eligible to suppress a human-authorized retry")
	}
	if len(loaded.deliveryAudit) != 2 || loaded.deliveryAudit[1].Resolution != wakeDeliveryResolutionAbandoned {
		t.Fatalf("abandon audit = %+v", loaded.deliveryAudit)
	}
}

func TestWakeNotifierKeepsAnInFlightDesktopDeliveryPrepared(t *testing.T) {
	output := &bytes.Buffer{}
	app := NewApp(t.TempDir(), output, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-WAKE-IN-FLIGHT")
	snapshot, err := app.Query.Snapshot(context.Background(), "T-WAKE-IN-FLIGHT", 0)
	if err != nil {
		t.Fatal(err)
	}
	key := wakeKey(snapshot)
	notifier := &wakeNotifier{
		app:          app,
		dryRun:       true,
		stallTimeout: time.Nanosecond,
		notified:     map[string]bool{key: true},
		wakeAt:       map[string]time.Time{key: app.Clock()},
		running:      map[string]bool{"reasonix": true},
		retryAfter:   map[string]time.Time{},
		deliveries: map[string]wakeDelivery{
			key: {
				ID:        wakeDeliveryID(key, "reasonix"),
				TaskID:    snapshot.Task.ID,
				Client:    "reasonix",
				Status:    wakeDeliveryPrepared,
				UpdatedAt: app.Clock(),
			},
		},
	}
	notifier.scan(context.Background())
	if got := notifier.deliveries[key].Status; got != wakeDeliveryPrepared {
		t.Fatalf("in-flight delivery status = %q, want %q", got, wakeDeliveryPrepared)
	}
}

func TestWakeDeliveryIDIsStablePerTaskStateAndClient(t *testing.T) {
	key := "TASK-42|ASSIGNED|3"
	if first, second := wakeDeliveryID(key, "reasonix"), wakeDeliveryID(key, "reasonix"); first != second {
		t.Fatalf("same delivery ids differ: %q vs %q", first, second)
	}
	if wakeDeliveryID(key, "reasonix") == wakeDeliveryID(key, "cc-haha") {
		t.Fatal("different clients reused a delivery id")
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

func TestWatchReasonixWorkProfileFlag(t *testing.T) {
	output := &bytes.Buffer{}
	app := NewApp(t.TempDir(), output, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, err := app.watch(context.Background(), []string{"--once", "--dry-run", "--reasonix-work-profile", reasonixWorkBalanced}, false)
	if err != nil || code != ExitOK {
		t.Fatalf("watch = (%d, %v), want success", code, err)
	}
	if !strings.Contains(output.String(), "reasonix=desktop(normal/balanced/auto)") {
		t.Fatalf("watch output = %q", output.String())
	}

	code, err = app.watch(context.Background(), []string{"--reasonix-work-profile", "unsupported"}, false)
	if code != ExitValidation || err == nil || !strings.Contains(err.Error(), "--reasonix-work-profile must be balanced or delivery") {
		t.Fatalf("invalid profile = (%d, %v), want validation error", code, err)
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
