package webconsole

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type recordingRunner struct {
	args [][]string
}

func (r *recordingRunner) RunJSON(_ context.Context, args []string) CommandResult {
	r.args = append(r.args, append([]string(nil), args...))
	return CommandResult{Code: 0, Output: json.RawMessage(`{"status":"ok"}`)}
}

type fixedReader struct{}

func (fixedReader) Overview(context.Context, string, string) (Overview, error) {
	return Overview{Initialized: true, Clients: []Client{{ID: "codex", Name: "Codex"}}, Tasks: []TaskSummary{{ID: "T-0001", Health: "HEALTHY", Status: "DRAFT", Version: 1}}}, nil
}

func (fixedReader) Task(context.Context, string, string, string) (TaskView, error) {
	return TaskView{Health: "HEALTHY", Task: Task{ID: "T-0001"}, State: State{TaskID: "T-0001", Status: "DRAFT", Version: 1}}, nil
}

func TestServerReadsAndRunsOnlyKnownCommands(t *testing.T) {
	runner := &recordingRunner{}
	server, err := NewServer(runner, fixedReader{})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	server.SetOrigin(httpServer.URL)

	response, err := http.Get(httpServer.URL + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	var session map[string]string
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if session["csrf_token"] == "" {
		t.Fatal("session did not include anti-CSRF token")
	}

	response, err = http.Get(httpServer.URL + "/api/v1/overview")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Security-Policy") == "" {
		response.Body.Close()
		t.Fatalf("overview status=%d headers=%v", response.StatusCode, response.Header)
	}
	response.Body.Close()

	response = postJSON(t, httpServer.URL+"/api/v1/clients", httpServer.URL, "", clientRequest{ID: "codex", Name: "Codex", Capabilities: []string{"review"}})
	if response.StatusCode != http.StatusForbidden {
		response.Body.Close()
		t.Fatalf("missing token status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = postJSON(t, httpServer.URL+"/api/v1/tasks/T-0001/actions/approve", httpServer.URL, session["csrf_token"], taskActionRequest{Actor: "codex", ExpectedVersion: 4})
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("action status=%d", response.StatusCode)
	}
	response.Body.Close()
	if len(runner.args) != 1 {
		t.Fatalf("runner calls=%v", runner.args)
	}
	want := []string{"review", "approve", "--task", "T-0001", "--actor", "codex", "--expected-version", "4"}
	if !reflect.DeepEqual(runner.args[0], want) {
		t.Fatalf("args=%v want=%v", runner.args[0], want)
	}

	response = postJSON(t, httpServer.URL+"/api/v1/tasks/T-0001/actions/message", httpServer.URL, session["csrf_token"], taskActionRequest{Actor: "codex", Feedback: "补充说明", ExpectedVersion: 5})
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("message action status=%d", response.StatusCode)
	}
	response.Body.Close()
	if len(runner.args) != 2 {
		t.Fatalf("runner calls=%v", runner.args)
	}
	messageWant := []string{"message", "add", "--task", "T-0001", "--actor", "codex", "--body", "补充说明", "--expected-version", "5"}
	if !reflect.DeepEqual(runner.args[1], messageWant) {
		t.Fatalf("message args=%v want=%v", runner.args[1], messageWant)
	}

	response = postJSON(t, httpServer.URL+"/api/v1/tasks/T-0001/actions/submit", httpServer.URL, session["csrf_token"], taskActionRequest{Actor: "cc-haha", ExpectedVersion: 4})
	if response.StatusCode != http.StatusNotFound {
		response.Body.Close()
		t.Fatalf("agent action status=%d", response.StatusCode)
	}
	response.Body.Close()
	if len(runner.args) != 2 {
		t.Fatalf("agent action unexpectedly ran=%v", runner.args)
	}

	response = postJSON(t, httpServer.URL+"/api/v1/tasks", httpServer.URL, session["csrf_token"], map[string]any{})
	if response.StatusCode != http.StatusNotFound {
		response.Body.Close()
		t.Fatalf("manual task creation endpoint status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = postJSON(t, httpServer.URL+"/api/v1/tasks/T-0001/evidence", httpServer.URL, session["csrf_token"], map[string]any{})
	if response.StatusCode != http.StatusNotFound {
		response.Body.Close()
		t.Fatalf("evidence endpoint status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = postJSON(t, httpServer.URL+"/api/v1/tasks/T-0001/handoff-next", httpServer.URL, session["csrf_token"], struct{}{})
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("handoff next status=%d", response.StatusCode)
	}
	response.Body.Close()
	if len(runner.args) != 3 || !reflect.DeepEqual(runner.args[2], []string{"handoff", "next", "--task", "T-0001"}) {
		t.Fatalf("handoff next args=%v", runner.args)
	}

	response = postJSON(t, httpServer.URL+"/api/v1/local-projects", httpServer.URL, session["csrf_token"], localProjectRequest{Path: `D:\projects\example`, Name: "Example"})
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("local project status=%d", response.StatusCode)
	}
	response.Body.Close()
	if len(runner.args) != 4 || !reflect.DeepEqual(runner.args[3], []string{"project", "register-local", "--path", `D:\projects\example`, "--name", "Example"}) {
		t.Fatalf("local project args=%v", runner.args)
	}

	response = postJSON(t, httpServer.URL+"/api/v1/handoffs", httpServer.URL, session["csrf_token"], map[string]any{})
	if response.StatusCode != http.StatusNotFound {
		response.Body.Close()
		t.Fatalf("manual handoff endpoint status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = postJSON(t, httpServer.URL+"/api/v1/exec", httpServer.URL, session["csrf_token"], map[string]any{"args": []string{"anything"}})
	if response.StatusCode != http.StatusNotFound {
		response.Body.Close()
		t.Fatalf("generic command endpoint status=%d", response.StatusCode)
	}
	response.Body.Close()
}

func TestListenLocalFallsBackWhenPreferredPortIsOccupied(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	listener, fallback, err := ListenLocal(occupied.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if !fallback {
		t.Fatal("ListenLocal did not fall back from an occupied address")
	}
	if listener.Addr().String() == occupied.Addr().String() {
		t.Fatalf("fallback address=%s", listener.Addr())
	}
	if _, _, err := net.SplitHostPort(listener.Addr().String()); err != nil {
		t.Fatalf("fallback address=%s err=%v", listener.Addr(), err)
	}
	if _, _, err := net.SplitHostPort("127.0.0.1:8567"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := net.SplitHostPort("0.0.0.0:8567"); err != nil {
		t.Fatal(err)
	}
	if err := requireLoopbackAddress("0.0.0.0:8567"); err == nil {
		t.Fatal("accepted a non-loopback address")
	}
}

func postJSON(t *testing.T, target, origin, token string, value any) *http.Response {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	if token != "" {
		request.Header.Set("X-Collab-CSRF", token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
