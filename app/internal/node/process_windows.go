//go:build windows

package node

import (
	"os/exec"
	"syscall"
)

func hideProcess(p *exec.Cmd) { p.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
