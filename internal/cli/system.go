package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alexgred/printpipe/internal/printer"
)

// System is everything of the computer the CLI touches: the file system, the
// programs and device files printing goes through, the network, the clock,
// and later the service manager and the releases. Run gets it injected, so
// that tests drive every command against a fake.
type System interface {
	printer.Computer
	// ConfigDir is the user's configuration folder, os.UserConfigDir.
	ConfigDir() (string, error)
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

func (OS) ConfigDir() (string, error)                   { return os.UserConfigDir() }
func (OS) ReadFile(name string) ([]byte, error)         { return os.ReadFile(name) }
func (OS) Listen(addr string) (net.Listener, error)     { return net.Listen("tcp", addr) }
func (OS) Do(req *http.Request) (*http.Response, error) { return client.Do(req) }
func (OS) Now() time.Time                               { return time.Now() }

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
func (OS) WriteDevice(ctx context.Context, path string, job []byte) error {
	done := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			done <- err
			return
		}
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
