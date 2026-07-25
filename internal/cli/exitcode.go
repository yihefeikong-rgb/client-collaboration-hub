package cli

import (
	"errors"
	"io"
	"os"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

const (
	ExitOK         = 0
	ExitValidation = 2
	ExitConflict   = 3
	ExitInternal   = 4
	ExitBinding    = 5
	ExitRecovery   = 6
	ExitCorrupt    = 7
	ExitUnknown    = 8
	ExitNotFound   = 9
)

func exitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, store.ErrVersionConflict):
		return ExitConflict
	case errors.Is(err, store.ErrRecoveryRequired):
		return ExitRecovery
	case errors.Is(err, store.ErrCorrupt):
		return ExitCorrupt
	case errors.Is(err, store.ErrCommitOutcomeUnknown):
		return ExitUnknown
	case errors.Is(err, store.ErrTaskNotFound), errors.Is(err, store.ErrNotFound):
		return ExitNotFound
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) || errors.Is(err, io.ErrShortWrite) {
		return ExitInternal
	}
	return ExitValidation
}
