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
