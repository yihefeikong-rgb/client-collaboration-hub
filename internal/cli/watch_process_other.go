//go:build !windows

package cli

import "os/exec"

func hideWatchCommandWindow(*exec.Cmd) {}
