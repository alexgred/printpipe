package cli

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"time"

	"github.com/alexgred/printpipe/internal/server"
)

// fakeSystem is the computer the CLI runs on in tests: a file system in
// memory, a listener on a free port, a clock that stands still, and whatever
// answers on the agent's port.
type fakeSystem struct {
	configDir string
	files     map[string][]byte
	// listened is the address the server asked for; the fake listens on a
	// free port instead.
	listened string
	// listening gets the listener once the server listens.
	listening chan net.Listener
	now       time.Time
	// onPort answers the CLI's requests to 127.0.0.1; nil is nothing
	// listening there.
	onPort http.Handler
	// requested is the URL of each request the CLI made.
	requested []string
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{
		configDir: "/home/me/.config",
		files:     map[string][]byte{},
		listening: make(chan net.Listener, 1),
		now:       time.Date(2026, 11, 3, 20, 15, 4, 0, time.UTC),
	}
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

func (s *fakeSystem) Now() time.Time { return s.now }

func (s *fakeSystem) Do(req *http.Request) (*http.Response, error) {
	s.requested = append(s.requested, req.URL.String())
	if s.onPort == nil {
		return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	}
	rec := httptest.NewRecorder()
	s.onPort.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// fakePrinter is the printer behind a running agent: it keeps each job, or
// fails with err.
type fakePrinter struct {
	jobs [][]byte
	err  error
}

func (p *fakePrinter) Print(_ context.Context, job []byte) error {
	if p.err != nil {
		return p.err
	}
	p.jobs = append(p.jobs, job)
	return nil
}

// runAgent makes a printpipe agent answer on the port, allowing origins and
// printing to p.
func (s *fakeSystem) runAgent(p *fakePrinter, origins ...string) {
	s.onPort = server.Handler(server.Options{Version: "dev", PID: 4242, Origins: origins}, p)
}

// runForeignProgram makes another program answer on the port.
func (s *fakeSystem) runForeignProgram() {
	s.onPort = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>hello</html>")
	})
}
