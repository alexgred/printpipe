package cli

import (
	"io/fs"
	"net"
)

// fakeSystem is the computer the CLI runs on in tests: a file system in
// memory and a listener on a free port.
type fakeSystem struct {
	configDir string
	files     map[string][]byte
	// listened is the address the server asked for; the fake listens on a
	// free port instead.
	listened string
	// listening gets the listener once the server listens.
	listening chan net.Listener
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{configDir: "/home/me/.config", files: map[string][]byte{}, listening: make(chan net.Listener, 1)}
}

func (s *fakeSystem) ConfigDir() (string, error) { return s.configDir, nil }

func (s *fakeSystem) ReadFile(name string) ([]byte, error) {
	b, ok := s.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return b, nil
}

func (s *fakeSystem) Listen(addr string) (net.Listener, error) {
	s.listened = addr
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		s.listening <- l
	}
	return l, err
}
