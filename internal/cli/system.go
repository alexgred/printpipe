package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/alexgred/printpipe/internal/printer"
)

// System is everything of the computer the CLI touches: the file system, the
// programs and device files printing goes through, the network, the clock,
// the processes of the agent, and the web the releases are on. Run gets it
// injected, so that tests drive every command against a fake.
type System interface {
	printer.Computer
	// GOARCH is the processor architecture, runtime.GOARCH.
	GOARCH() string
	// Redirect is where a URL redirects to, without going there. An answer that
	// is no redirect is an httpStatus.
	Redirect(ctx context.Context, url string) (string, error)
	// Download is what a URL gives, through its redirects. An answer other than
	// 200 is an httpStatus.
	Download(ctx context.Context, url string) ([]byte, error)
	// UID is the user's id, os.Getuid; launchd's session is named by it.
	UID() int
	// HomeDir is the user's home folder, os.UserHomeDir.
	HomeDir() (string, error)
	// ConfigDir is the user's configuration folder, os.UserConfigDir.
	ConfigDir() (string, error)
	// LocalAppData is %LOCALAPPDATA%; it is asked for on Windows only.
	LocalAppData() (string, error)
	// Executable is the path of the running printpipe, os.Executable.
	Executable() (string, error)
	// WriteFile replaces a file, creating its folder.
	WriteFile(name string, data []byte) error
	// WriteProgram is WriteFile for a file that is to be run.
	WriteProgram(name string, data []byte) error
	Remove(name string) error
	// RemoveAfterExit leaves files, then folders if they are empty, to a hidden
	// command that outlives this process and waits for whatever holds them: Windows
	// cannot delete a program that is running, and this one is. It says nothing of
	// how that went, and is asked for on Windows only.
	RemoveAfterExit(files, folders []string) error
	// Rename moves a file, replacing what is at to.
	Rename(from, to string) error
	// OpenAppend opens a file to add to, creating it and its folder, and
	// returns its size.
	OpenAppend(name string) (io.WriteCloser, int64, error)
	// Spawn starts a program that outlives this one: in its own session, with
	// no terminal.
	Spawn(name string, args ...string) error
	// Terminate asks a process to exit: SIGTERM, or on Windows the agent's stop
	// event, which the pid plays no part in.
	Terminate(pid int) error
	// Kill ends a process at once.
	Kill(pid int) error
	// StopRequests is closed when this process is asked to exit other than by
	// a signal: on Windows through the stop event, which it opens.
	StopRequests() (<-chan struct{}, error)
	// ProcessExe is the program file a process runs, when this user may see it.
	ProcessExe(pid int) (string, error)
	// HasRunValue, SetRunValue and DeleteRunValue are the value name in the
	// user's HKCU Run key, whose command Windows runs at logon. They are asked
	// for on Windows only. Deleting a value that is not there is fs.ErrNotExist.
	HasRunValue(name string) bool
	SetRunValue(name, command string) error
	DeleteRunValue(name string) error
	// PathEnv is this process's PATH.
	PathEnv() string
	// UserPath and SetUserPath are the Path in the user's HKCU Environment key,
	// as it is written there, %VARIABLES% unexpanded. They are asked for on
	// Windows only. A Path that is not there is empty.
	UserPath() (string, error)
	SetUserPath(value string) error
	Sleep(d time.Duration)
	// Listen opens the server's TCP listener on addr.
	Listen(addr string) (net.Listener, error)
	// Do sends an HTTP request, such as one to the running agent.
	Do(req *http.Request) (*http.Response, error)
	Now() time.Time
}

// OS is the real computer.
type OS struct{}

// client waits longer than the agent's own ~10 s for a printer to take a job.
var client = &http.Client{Timeout: 30 * time.Second}

// redirectClient stops at the first answer, which names the redirect.
var redirectClient = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// downloadClient gives a release on a slow connection time to arrive: the timeout
// also covers reading the body.
var downloadClient = &http.Client{Timeout: 10 * time.Minute}

// httpStatus is an answer that was not the one asked for: the web was reached.
type httpStatus int

func (s httpStatus) Error() string { return fmt.Sprintf("HTTP %d", int(s)) }

func (OS) Redirect(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := redirectClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	location := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || location == "" {
		return "", httpStatus(resp.StatusCode)
	}
	return location, nil
}

func (OS) Download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatus(resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (OS) GOARCH() string                               { return runtime.GOARCH }
func (OS) GOOS() string                                 { return runtime.GOOS }
func (OS) UID() int                                     { return os.Getuid() }
func (OS) HomeDir() (string, error)                     { return os.UserHomeDir() }
func (OS) ConfigDir() (string, error)                   { return os.UserConfigDir() }
func (OS) LocalAppData() (string, error)                { return os.UserCacheDir() }
func (OS) Executable() (string, error)                  { return os.Executable() }
func (OS) ReadFile(name string) ([]byte, error)         { return os.ReadFile(name) }
func (OS) Remove(name string) error                     { return os.Remove(name) }
func (OS) Rename(from, to string) error                 { return os.Rename(from, to) }
func (OS) Listen(addr string) (net.Listener, error)     { return net.Listen("tcp", addr) }
func (OS) Do(req *http.Request) (*http.Response, error) { return client.Do(req) }
func (OS) Now() time.Time                               { return time.Now() }
func (OS) Sleep(d time.Duration)                        { time.Sleep(d) }
func (OS) PathEnv() string                              { return os.Getenv("PATH") }

func (OS) Kill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()
	return p.Kill()
}

func (OS) WriteFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// WriteProgram also makes a file left by an earlier try runnable, whatever mode
// it has.
func (OS) WriteProgram(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(name, data, 0o755); err != nil {
		return err
	}
	return os.Chmod(name, 0o755)
}

func (OS) OpenAppend(name string) (io.WriteCloser, int64, error) {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (OS) Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	// English, for /print's messages and for the words printpipe reads.
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return out, fmt.Errorf("%s: %w: %s", name, err, said)
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

func (OS) ReadDir(name string) ([]string, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names, nil
}

// WriteDevice leaves a write that does not finish by the deadline behind: a
// write to a printer that stopped taking data cannot be interrupted, and it
// ends when the printer takes the data or goes away.
//
// The device is opened with syscall.Open because os.OpenFile makes a device
// that can be polled non-blocking: usblp then lost the job, with no error, as
// soon as the file was closed.
func (OS) WriteDevice(ctx context.Context, path string, job []byte) error {
	done := make(chan error, 1)
	go func() {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CLOEXEC, 0)
		if err != nil {
			done <- &fs.PathError{Op: "open", Path: path, Err: err}
			return
		}
		f := os.NewFile(uintptr(fd), path)
		_, err = f.Write(job)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
