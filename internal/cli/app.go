package cli

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type App struct {
	Root     string
	Stdout   io.Writer
	Stderr   io.Writer
	Clock    func() time.Time
	Registry store.RegistryStore
	Journal  store.TaskJournal
}

func NewApp(root string, stdout, stderr io.Writer, clock func() time.Time) *App {
	if clock == nil {
		clock = time.Now
	}
	dataRoot := filepath.Join(root, "collaboration")
	registry := store.NewFileRegistryStore(dataRoot, store.FlockLocker{})
	evidence := store.NewFileEvidenceStore(dataRoot)
	return &App{
		Root:     root,
		Stdout:   stdout,
		Stderr:   stderr,
		Clock:    clock,
		Registry: registry,
		Journal:  store.NewFileTaskJournal(dataRoot, store.FlockLocker{}, registry, evidence),
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
