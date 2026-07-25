package cli

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/handoff"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type App struct {
	Root     string
	Stdout   io.Writer
	Stderr   io.Writer
	Clock    func() time.Time
	Registry store.RegistryStore
	Bindings store.BindingStore
	Journal  store.TaskJournal
	Query    store.TaskQuery
	Handoff  *handoff.Service
}

type AppConfig struct {
	MaxHashFileSize int64
}

func NewApp(root string, stdout, stderr io.Writer, clock func() time.Time) *App {
	return NewAppWithConfig(root, stdout, stderr, clock, AppConfig{})
}

func NewAppWithConfig(root string, stdout, stderr io.Writer, clock func() time.Time, config AppConfig) *App {
	if clock == nil {
		clock = time.Now
	}
	dataRoot := filepath.Join(root, "collaboration")
	registry := store.NewFileRegistryStore(dataRoot, store.FlockLocker{})
	evidence := store.NewFileEvidenceStore(dataRoot)
	journal := store.NewFileTaskJournal(dataRoot, store.FlockLocker{}, registry, evidence)
	bindings := store.NewFileBindingStore(dataRoot, store.FlockLocker{}, registry)
	query := store.NewFileTaskQuery(journal, registry)
	return &App{
		Root:     root,
		Stdout:   stdout,
		Stderr:   stderr,
		Clock:    clock,
		Registry: registry,
		Bindings: bindings,
		Journal:  journal,
		Query:    query,
		Handoff:  handoff.NewService(query, bindings, store.NewFileBindingResolver(store.BindingResolverConfig{MaxHashFileSize: config.MaxHashFileSize}), registry, root),
	}
}

func (a *App) Run(args []string) int {
	jsonOutput, args, err := extractJSONFlag(args)
	if err != nil {
		a.writeError(false, err)
		return ExitValidation
	}
	if len(args) == 0 {
		a.writeError(jsonOutput, errUsage)
		return ExitValidation
	}
	ctx := context.Background()
	code, err := a.run(ctx, args, jsonOutput)
	if err != nil {
		a.writeError(jsonOutput, err)
	}
	return code
}

func (a *App) now() time.Time {
	return a.Clock().UTC()
}
