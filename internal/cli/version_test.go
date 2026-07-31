package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/version"
)

func TestVersionCommandTextOutput(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	app := NewApp(root, &stdout, &stderr, nil)
	if code := app.Run([]string{"version"}); code != ExitOK {
		t.Fatalf("version: code=%d stderr=%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != version.Version {
		t.Fatalf("version output = %q, want %q", got, version.Version)
	}
}

func TestVersionCommandJSONOutput(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	app := NewApp(root, &stdout, &stderr, nil)
	if code := app.Run([]string{"--json", "version"}); code != ExitOK {
		t.Fatalf("--json version: code=%d stderr=%s", code, stderr.String())
	}
	var output map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("non-JSON stdout %q: %v", stdout.String(), err)
	}
	if output["version"] != version.Version {
		t.Fatalf("JSON version = %q, want %q", output["version"], version.Version)
	}
}

func TestVersionCommandInvalidArgsRejected(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	app := NewApp(root, &stdout, &stderr, nil)
	if code := app.Run([]string{"version", "extra"}); code != ExitValidation {
		t.Fatalf("version extra: code=%d, want ExitValidation", code)
	}
}
