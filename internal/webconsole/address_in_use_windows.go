//go:build windows

package webconsole

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isAddressInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE)
}
