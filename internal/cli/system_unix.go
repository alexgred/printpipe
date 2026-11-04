//go:build !windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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

// ProcessExe reads /proc on Linux, where the link of a program replaced while
// running ends in " (deleted)". A Mac has no /proc, so lsof tells, which also
// fails for another user's process, where ps would answer.
func (o OS) ProcessExe(pid int) (string, error) {
	if runtime.GOOS == "darwin" {
		out, err := o.Run(context.Background(), nil, "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn")
		if err != nil {
			return "", err
		}
		return lsofProgram(string(out))
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	return strings.TrimSuffix(exe, " (deleted)"), err
}

var errNoProgram = errors.New("lsof names no program file")

// lsofProgram is the program file in what lsof -Fn says of a process's text
// files: the first name, on a line of its own after an n.
func lsofProgram(out string) (string, error) {
	for line := range strings.Lines(out) {
		if name, ok := strings.CutPrefix(line, "n"); ok {
			return strings.TrimSuffix(name, "\n"), nil
		}
	}
	return "", errNoProgram
}

// StopRequests is never closed: SIGTERM asks a process to exit here.
func (OS) StopRequests() (<-chan struct{}, error) { return nil, nil }

// Only Windows keeps a running program from being removed.

func (OS) RemoveAfterExit([]string, []string) error { return errors.ErrUnsupported }

// Only Windows has a Run key.

func (OS) HasRunValue(string) bool          { return false }
func (OS) SetRunValue(string, string) error { return errors.ErrUnsupported }
func (OS) DeleteRunValue(string) error      { return errors.ErrUnsupported }

// Only Windows keeps the user's Path in its registry.

func (OS) UserPath() (string, error) { return "", errors.ErrUnsupported }
func (OS) SetUserPath(string) error  { return errors.ErrUnsupported }
