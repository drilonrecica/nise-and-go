//go:build unix

package dev

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in a process group of its own, so that a
// terminal's Ctrl-C reaches only nise and so that Stop can signal the
// child's own descendants along with it.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup sends SIGTERM to the child's whole process group. The
// negative pid is what makes it a group signal: a `pnpm` that forked
// `vite` is in the same group and receives it too.
func terminateGroup(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGTERM)
}

// killGroup sends SIGKILL to the child's whole process group.
func killGroup(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}

// isProcessGone reports whether err means the target already exited, which
// is success for a stop, not a failure.
func isProcessGone(err error) bool {
	return errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone)
}
