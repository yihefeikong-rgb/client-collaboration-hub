package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestUILaunchesLocalConsoleAndWritesThroughCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout lockedBuffer
	app := NewApp(t.TempDir(), &stdout, io.Discard, time.Now)
	result := make(chan struct {
		code int
		err  error
	}, 1)
	go func() {
		code, err := app.ui(ctx, nil, false)
		result <- struct {
			code int
			err  error
		}{code: code, err: err}
	}()

	url := waitForConsoleURL(t, &stdout)
	client := &http.Client{Timeout: time.Second}
	var session struct {
		Token string `json:"csrf_token"`
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		response, err := client.Get(url + "api/v1/session")
		if err == nil {
			decodeErr := json.NewDecoder(response.Body).Decode(&session)
			response.Body.Close()
			if decodeErr == nil && session.Token != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("console did not answer at %s; output=%q", url, stdout.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	request, err := http.NewRequest(http.MethodPost, url+"api/v1/init", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", strings.TrimSuffix(url, "/"))
	request.Header.Set("X-Collab-CSRF", session.Token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var initResult struct {
		OK bool `json:"ok"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&initResult)
	response.Body.Close()
	if decodeErr != nil || !initResult.OK {
		t.Fatalf("init response decode=%v result=%+v", decodeErr, initResult)
	}

	response, err = client.Get(url + "api/v1/overview")
	if err != nil {
		t.Fatal(err)
	}
	var overview struct {
		Initialized bool `json:"initialized"`
	}
	decodeErr = json.NewDecoder(response.Body).Decode(&overview)
	response.Body.Close()
	if decodeErr != nil || !overview.Initialized {
		t.Fatalf("overview decode=%v result=%+v", decodeErr, overview)
	}

	cancel()
	select {
	case outcome := <-result:
		if outcome.err != nil || outcome.code != ExitOK {
			t.Fatalf("ui stopped code=%d err=%v", outcome.code, outcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ui did not stop after context cancellation")
	}
}

func TestConsoleOverviewDoesNotInitializeFilesystem(t *testing.T) {
	root := t.TempDir()
	app := NewApp(root, io.Discard, io.Discard, time.Now)
	reader := appConsoleReader{app: app}
	overview, err := reader.Overview(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if overview.Initialized || len(overview.Clients) != 0 || len(overview.Projects) != 0 || len(overview.Tasks) != 0 {
		t.Fatalf("overview=%+v", overview)
	}
	if _, err := os.Stat(filepath.Join(root, "collaboration")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only overview created collaboration directory: %v", err)
	}
	if _, err := reader.Task(context.Background(), "T-0001", "", ""); !errors.Is(err, store.ErrTaskNotFound) {
		t.Fatalf("missing task error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "collaboration")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing task lookup created collaboration directory: %v", err)
	}
}

func TestConsoleProjectIncludesPolicyData(t *testing.T) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	agentPolicy := protocol.CollaborationPolicy{
		SubmissionMode: protocol.SubmissionModeAgentAuto,
		FinalReview:    protocol.FinalReviewAgent,
		AutoDone:       true,
	}
	project := protocol.Project{
		ID:                  "demo",
		Name:                "Demo",
		CreatedAt:           now,
		CollaborationPolicy: agentPolicy,
		PolicyVersion:       2,
		PolicyHistory: []protocol.PolicyAuditEntry{
			{
				Version:  2,
				Actor:    "codex",
				Origin:   protocol.EventOriginHuman,
				At:       now.Add(time.Hour),
				Previous: protocol.DefaultCollaborationPolicy(),
				Current:  agentPolicy,
			},
		},
	}
	view := consoleProject(project)
	if view.FinalReview != "agent" || !view.AutoDone || view.PolicyVersion != 2 {
		t.Fatalf("console project policy view=%+v", view)
	}
	if view.RecentPolicyAudit == nil {
		t.Fatal("expected recent policy audit")
	}
	audit := view.RecentPolicyAudit
	if audit.Version != 2 || audit.Actor != "codex" || audit.Previous.FinalReview != "human" || audit.Previous.AutoDone || audit.Current.FinalReview != "agent" || !audit.Current.AutoDone {
		t.Fatalf("recent policy audit=%+v", audit)
	}
}

func TestConsoleProjectWithoutHistoryHasNilAudit(t *testing.T) {
	view := consoleProject(protocol.Project{})
	if view.FinalReview != "" || view.AutoDone || view.PolicyVersion != 0 || view.RecentPolicyAudit != nil {
		t.Fatalf("console project without policy=%+v", view)
	}
}

func TestConsoleOverviewExposesProjectPolicyData(t *testing.T) {
	now := time.Now().UTC()
	app := NewApp(t.TempDir(), io.Discard, io.Discard, func() time.Time { return now })
	reader := appConsoleReader{app: app}
	ctx := context.Background()

	agentPolicy := protocol.CollaborationPolicy{
		SubmissionMode: protocol.SubmissionModeAgentAuto,
		FinalReview:    protocol.FinalReviewAgent,
		AutoDone:       true,
	}
	if err := app.Registry.CreateProject(ctx, protocol.Project{ID: "demo", Name: "Demo", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	updated, err := app.Registry.UpdateProjectPolicy(ctx, "demo", 1, "codex", agentPolicy, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("policy version=%d", updated.PolicyVersion)
	}

	overview, err := reader.Overview(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Projects) != 1 {
		t.Fatalf("projects=%d", len(overview.Projects))
	}
	view := overview.Projects[0]
	if view.FinalReview != "agent" || !view.AutoDone || view.PolicyVersion != 2 {
		t.Fatalf("project policy view=%+v", view)
	}
	if view.RecentPolicyAudit == nil || view.RecentPolicyAudit.Current.FinalReview != "agent" || view.RecentPolicyAudit.Previous.FinalReview != "human" {
		t.Fatalf("project policy audit=%+v", view.RecentPolicyAudit)
	}
}

func TestConsoleReasonHidesLocalPaths(t *testing.T) {
	if got := consoleReason(`open D:\work\candidate.json: file does not exist`); strings.Contains(got, `D:\work`) {
		t.Fatalf("console reason leaked local path: %q", got)
	}
	if got := consoleReason("version conflict"); got != "version conflict" {
		t.Fatalf("console reason changed safe message: %q", got)
	}
}

func waitForConsoleURL(t *testing.T, output *lockedBuffer) string {
	t.Helper()
	pattern := regexp.MustCompile(`http://127\.0\.0\.1:\d+/`)
	for deadline := time.Now().Add(5 * time.Second); ; {
		if url := pattern.FindString(output.String()); url != "" {
			return url
		}
		if time.Now().After(deadline) {
			t.Fatalf("console URL was not printed: %q", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
