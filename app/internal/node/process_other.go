//go:build !windows

package node

import "os/exec"

func hideProcess(_ *exec.Cmd) {}
