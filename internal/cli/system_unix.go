//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func (OS) Spawn(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Dir = "/" // holds no folder of the terminal it was started from
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (OS) Terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// ProcessExe reads /proc, so it knows only Linux's processes: only Linux uses
// it. The link of a program replaced while running ends in " (deleted)".
func (OS) ProcessExe(pid int) (string, error) {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	return strings.TrimSuffix(exe, " (deleted)"), err
}

// StopRequests is never closed: SIGTERM asks a process to exit here.
func (OS) StopRequests() (<-chan struct{}, error) { return nil, nil }

// Only Windows has a Run key.

func (OS) HasRunValue(string) bool          { return false }
func (OS) SetRunValue(string, string) error { return errors.ErrUnsupported }
func (OS) DeleteRunValue(string) error      { return errors.ErrUnsupported }
