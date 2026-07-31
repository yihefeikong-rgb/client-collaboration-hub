//go:build !windows

package webconsole

import (
	"errors"
	"syscall"
)

func isAddressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
