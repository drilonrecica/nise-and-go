//go:build windows

package dev

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup gives the child its own console process group, so a
// Ctrl-C delivered to nise's console is not also delivered to the child and
// shutdown stays ordered.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// terminateGroup ends the child.
//
// Windows has no SIGTERM. GenerateConsoleCtrlEvent can deliver a Ctrl-Break
// to a process group, but only a console application in the same console
// receives it, and neither `go` nor `pnpm` is guaranteed to translate it
// into a graceful shutdown. Rather than pretend a graceful path exists,
// this terminates the process directly; Stop's grace period therefore has
// no effect on Windows, which docs/commands/dev.md states plainly.
func terminateGroup(p *os.Process) error {
	return p.Kill()
}

// killGroup ends the child unconditionally.
func killGroup(p *os.Process) error {
	return p.Kill()
}

// isProcessGone reports whether err means the target already exited.
func isProcessGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.Errno(87))
}
