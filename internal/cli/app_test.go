package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

func TestCLIWorkflowFromInitToDone(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	now := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	app := NewApp(root, &stdout, &stderr, func() time.Time { return now })
	run := func(args ...string) {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if code := app.Run(append([]string{"--json"}, args...)); code != ExitOK {
			t.Fatalf("%v: code=%d stderr=%s", args, code, stderr.String())
		}
		if !json.Valid(stdout.Bytes()) {
			t.Fatalf("%v: non-JSON stdout %q", args, stdout.String())
		}
	}
	run("init")
	run("client", "register", "--id", "codex", "--name", "Codex", "--capability", "create_task", "--capability", "review")
	run("client", "register", "--id", "cc-haha", "--name", "CC-HAHA", "--capability", "execute")
	run("project", "create", "--id", "project-1", "--name", "Demo")
	run("task", "create", "--id", "T-0001", "--project", "project-1", "--title", "Demo task", "--objective", "Verify workflow", "--acceptance", "Tests pass", "--creator", "codex")
	run("task", "assign", "--task", "T-0001", "--client", "cc-haha", "--expected-version", "1")
	run("task", "accept", "--task", "T-0001", "--actor", "cc-haha", "--expected-version", "2")
	run("evidence", "add", "--task", "T-0001", "--id", "E-diff-1", "--kind", "diff", "--summary", "Initial diff", "--created-by", "cc-haha", "--file-ref", "changes/initial.diff", "--expected-version", "3")
	run("evidence", "add", "--task", "T-0001", "--id", "E-test-1", "--kind", "test", "--summary", "Initial tests", "--created-by", "cc-haha", "--file-ref", "reports/test.txt", "--expected-version", "4")
	run("task", "submit", "--task", "T-0001", "--actor", "cc-haha", "--evidence", "E-diff-1", "--evidence", "E-test-1", "--expected-version", "5")
	run("review", "request-changes", "--task", "T-0001", "--actor", "codex", "--body", "Add revision", "--expected-version", "6")
	run("task", "resume", "--task", "T-0001", "--actor", "cc-haha", "--expected-version", "7")
	run("evidence", "add", "--task", "T-0001", "--id", "E-diff-2", "--kind", "diff", "--summary", "Revised diff", "--created-by", "cc-haha", "--file-ref", "changes/revised.diff", "--expected-version", "8")
	run("task", "submit", "--task", "T-0001", "--actor", "cc-haha", "--evidence", "E-diff-2", "--evidence", "E-test-1", "--expected-version", "9")
	run("review", "approve", "--task", "T-0001", "--actor", "codex", "--expected-version", "10")
	run("status", "--task", "T-0001")
	var output struct {
		Health string `json:"health"`
		State  struct {
			Status  string `json:"status"`
			Version int64  `json:"version"`
		} `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.Health != string(store.Healthy) || output.State.Status != "DONE" || output.State.Version != 11 {
		t.Fatalf("status output = %s", stdout.String())
	}
}

func TestInitIsIdempotentAndPreservesGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("custom-entry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp(root, &bytes.Buffer{}, &bytes.Buffer{}, func() time.Time { return time.Now() })
	if app.Run([]string{"init"}) != ExitOK || app.Run([]string{"init"}) != ExitOK {
		t.Fatal("init is not idempotent")
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"custom-entry", "collaboration/.runtime/", "collaboration/bindings/", "collab.exe"} {
		if strings.Count(string(data), line) != 1 {
			t.Fatalf("gitignore content = %q", data)
		}
	}
}

func TestExitCodesAndOutcomeUnknownGuidance(t *testing.T) {
	for errorValue, want := range map[error]int{
		store.ErrVersionConflict:      ExitConflict,
		store.ErrRecoveryRequired:     ExitRecovery,
		store.ErrCorrupt:              ExitCorrupt,
		store.ErrCommitOutcomeUnknown: ExitUnknown,
		store.ErrTaskNotFound:         ExitNotFound,
		store.ErrNotFound:             ExitNotFound,
	} {
		if got := exitCode(errorValue); got != want {
			t.Fatalf("exitCode(%v) = %d, want %d", errorValue, got, want)
		}
	}
	var stderr bytes.Buffer
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &stderr, nil)
	app.writeError(false, store.ErrCommitOutcomeUnknown)
	if !strings.Contains(stderr.String(), "status before retrying") {
		t.Fatalf("outcome unknown guidance = %q", stderr.String())
	}
	if code := app.Run([]string{"status", "--task", "T-404"}); code != ExitNotFound {
		t.Fatalf("missing status exit code = %d", code)
	}
	if !errors.Is(store.ErrTaskNotFound, store.ErrTaskNotFound) {
		t.Fatal("sentinel regression")
	}
}

func TestCLIAuxiliaryCommandsAndFailures(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	app := NewApp(root, &stdout, &stderr, func() time.Time { return time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC) })
	run := func(want int, args ...string) {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if code := app.Run(args); code != want {
			t.Fatalf("%v: code=%d want=%d stderr=%s", args, code, want, stderr.String())
		}
	}
	run(ExitOK, "init")
	run(ExitOK, "client", "register", "--id", "codex", "--name", "Codex", "--capability", "create_task", "--capability", "review")
	run(ExitValidation, "client", "register", "--id", "codex", "--name", "Codex", "--capability", "review")
	run(ExitOK, "client", "register", "--id", "cc-haha", "--name", "CC-HAHA", "--capability", "execute")
	run(ExitOK, "project", "create", "--id", "project-1", "--name", "Demo")
	run(ExitOK, "task", "create", "--id", "T-0002", "--project", "project-1", "--title", "Block task", "--objective", "Test block", "--acceptance", "Pass", "--creator", "codex")
	run(ExitOK, "message", "add", "--task", "T-0002", "--actor", "codex", "--body", "Created", "--expected-version", "1")
	run(ExitConflict, "task", "assign", "--task", "T-0002", "--client", "cc-haha", "--expected-version", "1")
	run(ExitOK, "task", "assign", "--task", "T-0002", "--client", "cc-haha", "--expected-version", "2")
	run(ExitValidation, "evidence", "add", "--task", "T-0002", "--id", "E-bad", "--kind", "invalid", "--summary", "Bad", "--created-by", "cc-haha", "--expected-version", "3")
	run(ExitOK, "evidence", "add", "--task", "T-0002", "--id", "E-block", "--kind", "blocker", "--summary", "Blocked", "--created-by", "cc-haha", "--expected-version", "3")
	run(ExitOK, "task", "block", "--task", "T-0002", "--actor", "cc-haha", "--evidence", "E-block", "--expected-version", "4")
	run(ExitOK, "recover", "--task", "T-0002")
}

func TestCLIStatusAndRecoverHandleRecoverableTail(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	app := NewApp(root, &stdout, &stderr, func() time.Time { return time.Date(2026, 7, 25, 2, 0, 0, 0, time.UTC) })
	run := func(want int, args ...string) {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if code := app.Run(args); code != want {
			t.Fatalf("%v: code=%d want=%d stderr=%s", args, code, want, stderr.String())
		}
	}
	run(ExitOK, "init")
	run(ExitOK, "client", "register", "--id", "codex", "--name", "Codex", "--capability", "create_task", "--capability", "review")
	run(ExitOK, "client", "register", "--id", "cc-haha", "--name", "CC-HAHA", "--capability", "execute")
	run(ExitOK, "project", "create", "--id", "project-1", "--name", "Demo")
	run(ExitOK, "task", "create", "--id", "T-0003", "--project", "project-1", "--title", "Recover task", "--objective", "Recover", "--acceptance", "Pass", "--creator", "codex")
	journal := app.Journal.(*store.FileTaskJournal)
	journal.Replacer = cliFailingReplacer{}
	run(ExitInternal, "task", "assign", "--task", "T-0003", "--client", "cc-haha", "--expected-version", "1")
	journal.Replacer = cliOSReplacer{}
	run(ExitRecovery, "status", "--task", "T-0003")
	run(ExitOK, "recover", "--task", "T-0003")
	run(ExitOK, "status", "--task", "T-0003")
}

type cliFailingReplacer struct{}

func (cliFailingReplacer) Replace(_, path string) error {
	return &os.PathError{Op: "rename", Path: path, Err: errors.New("replace failed")}
}

type cliOSReplacer struct{}

func (cliOSReplacer) Replace(tempPath, targetPath string) error {
	return os.Rename(tempPath, targetPath)
}
