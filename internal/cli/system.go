package cli

import (
	"net"
	"net/http"
	"os"
	"time"
)

// System is everything of the computer the CLI touches: the file system, the
// network, the clock, and later the service manager, the printers and the
// releases. Run gets it injected, so that tests drive every command against a
// fake.
type System interface {
	// ConfigDir is the user's configuration folder, os.UserConfigDir.
	ConfigDir() (string, error)
	ReadFile(name string) ([]byte, error)
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
