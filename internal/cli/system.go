package cli

import (
	"net"
	"os"
)

// System is everything of the computer the CLI touches: the file system, the
// network, and later the service manager, the printers and the releases. Run
// gets it injected, so that tests drive every command against a fake.
type System interface {
	// ConfigDir is the user's configuration folder, os.UserConfigDir.
	ConfigDir() (string, error)
	ReadFile(name string) ([]byte, error)
	// Listen opens the server's TCP listener on addr.
	Listen(addr string) (net.Listener, error)
}

// OS is the real computer.
type OS struct{}

func (OS) ConfigDir() (string, error)               { return os.UserConfigDir() }
func (OS) ReadFile(name string) ([]byte, error)     { return os.ReadFile(name) }
func (OS) Listen(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
