package webconsole

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAddress = "127.0.0.1:8567"
	maxJSONBytes   = 1 << 20
)

//go:embed assets/*
var assets embed.FS

type Server struct {
	runner CommandRunner
	reader Reader

	csrfToken string
	mu        sync.RWMutex
	origin    string
}

func NewServer(runner CommandRunner, reader Reader) (*Server, error) {
	if runner == nil || reader == nil {
		return nil, errors.New("web console requires runner and reader")
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("generate anti-CSRF token: %w", err)
	}
	return &Server{runner: runner, reader: reader, csrfToken: hex.EncodeToString(token)}, nil
}

func ListenLocal(preferred string) (net.Listener, bool, error) {
	if err := requireLoopbackAddress(preferred); err != nil {
		return nil, false, err
	}
	listener, err := net.Listen("tcp", preferred)
	if err == nil {
		return listener, false, nil
	}
	if !isAddressInUse(err) {
		return nil, false, err
	}
	fallback, fallbackErr := net.Listen("tcp", "127.0.0.1:0")
	if fallbackErr != nil {
		return nil, false, fallbackErr
	}
	return fallback, true, nil
}

func requireLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if host != "127.0.0.1" {
		return errors.New("web console only permits 127.0.0.1")
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) SetOrigin(origin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.origin = origin
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	s.SetOrigin("http://" + listener.Addr().String())
	httpServer := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
		case <-done:
		}
	}()
	err := httpServer.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.setHeaders(w)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		s.serveAsset(w, "index.html")
	case r.Method == http.MethodGet && r.URL.Path == "/assets/app.js":
		s.serveAsset(w, "app.js")
	case r.Method == http.MethodGet && r.URL.Path == "/assets/style.css":
		s.serveAsset(w, "style.css")
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session":
		s.writeJSON(w, http.StatusOK, map[string]string{"csrf_token": s.csrfToken})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/overview":
		s.handleOverview(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
		s.handleTask(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/init":
		s.handleInit(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/clients":
		s.handleClient(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects":
		s.handleProject(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/local-projects":
		s.handleLocalProject(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/bindings":
		s.handleBinding(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
		s.handleTaskWrite(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/response-validations":
		s.handleResponseValidation(w, r)
	default:
		s.writeError(w, http.StatusNotFound, "not found")
	}
}

type localProjectRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Server) handleLocalProject(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var request localProjectRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	args := []string{"project", "register-local", "--path", request.Path}
	if request.ID != "" {
		args = append(args, "--id", request.ID)
	}
	if request.Name != "" {
		args = append(args, "--name", request.Name)
	}
	s.run(w, r, args)
}

func (s *Server) setHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func (s *Server) serveAsset(w http.ResponseWriter, name string) {
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "console asset is unavailable")
		return
	}
	contentType := mime.TypeByExtension("." + strings.TrimPrefix(name, "index."))
	switch name {
	case "index.html":
		contentType = "text/html; charset=utf-8"
	case "app.js":
		contentType = "text/javascript; charset=utf-8"
	case "style.css":
		contentType = "text/css; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.reader.Overview(r.Context(), r.URL.Query().Get("actor"), r.URL.Query().Get("device"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "无法读取本机协作数据；请在终端查看诊断。")
		return
	}
	s.writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	if taskID == "" || strings.Contains(taskID, "/") {
		s.writeError(w, http.StatusNotFound, "not found")
		return
	}
	view, err := s.reader.Task(r.Context(), taskID, r.URL.Query().Get("actor"), r.URL.Query().Get("device"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "无法读取该任务；请在终端查看诊断。")
		return
	}
	s.writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	s.run(w, r, []string{"init"})
}

type clientRequest struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

func (s *Server) handleClient(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var request clientRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	args := []string{"client", "register", "--id", request.ID, "--name", request.Name}
	for _, capability := range request.Capabilities {
		args = append(args, "--capability", capability)
	}
	s.run(w, r, args)
}

type projectRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var request projectRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	s.run(w, r, []string{"project", "create", "--id", request.ID, "--name", request.Name})
}

type bindingRequest struct {
	Project  string `json:"project"`
	Device   string `json:"device"`
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

func (s *Server) handleBinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var request bindingRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	args := []string{"project", "bind", "--project", request.Project, "--device", request.Device, "--path", request.Path}
	if request.Revision != "" {
		args = append(args, "--revision", request.Revision)
	}
	s.run(w, r, args)
}

type taskActionRequest struct {
	Actor           string `json:"actor"`
	Feedback        string `json:"feedback"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Server) handleTaskWrite(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		s.writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch {
	case len(parts) == 2 && parts[1] == "handoff-next":
		s.handleHandoffNext(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "actions" && parts[2] != "":
		s.handleAction(w, r, parts[0], parts[2])
	default:
		s.writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handleHandoffNext(w http.ResponseWriter, r *http.Request, taskID string) {
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	s.run(w, r, []string{"handoff", "next", "--task", taskID})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request, taskID, action string) {
	var request taskActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	version := strconv.FormatInt(request.ExpectedVersion, 10)
	var args []string
	switch action {
	case "request_changes":
		args = []string{"review", "request-changes", "--task", taskID, "--actor", request.Actor, "--body", request.Feedback, "--expected-version", version}
	case "approve":
		args = []string{"review", "approve", "--task", taskID, "--actor", request.Actor, "--expected-version", version}
	case "message":
		args = []string{"message", "add", "--task", taskID, "--actor", request.Actor, "--body", request.Feedback, "--expected-version", version}
	default:
		s.writeError(w, http.StatusNotFound, "unknown action")
		return
	}
	s.run(w, r, args)
}

type responseValidationRequest struct {
	Package string `json:"package"`
	Input   string `json:"input"`
}

func (s *Server) handleResponseValidation(w http.ResponseWriter, r *http.Request) {
	if !s.requireWrite(w, r) {
		return
	}
	var request responseValidationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	s.run(w, r, []string{"response", "validate", "--package", request.Package, "--input", request.Input})
}

func (s *Server) requireWrite(w http.ResponseWriter, r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		s.writeError(w, http.StatusUnsupportedMediaType, "application/json is required")
		return false
	}
	s.mu.RLock()
	origin := s.origin
	s.mu.RUnlock()
	if origin == "" || r.Header.Get("Origin") != origin {
		s.writeError(w, http.StatusForbidden, "invalid origin")
		return false
	}
	if r.Header.Get("X-Collab-CSRF") != s.csrfToken {
		s.writeError(w, http.StatusForbidden, "invalid anti-CSRF token")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "request body must contain one JSON value")
		return false
	}
	return true
}

func (s *Server) run(w http.ResponseWriter, r *http.Request, args []string) {
	result := s.runner.RunJSON(r.Context(), args)
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": result.Code == 0, "result": result})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	writeJSONError(w, status, message)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
