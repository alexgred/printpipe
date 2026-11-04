package cli

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/server"
)

// fakeSystem is the computer the CLI runs on in tests: a file system in
// memory, a listener on a free port, a clock that stands still, and whatever
// answers on the agent's port.
type fakeSystem struct {
	goos      string
	uid       int
	home      string
	configDir string
	// executable is the path of the running printpipe.
	executable string
	files      map[string][]byte
	// listened is the address the server asked for; the fake listens on a
	// free port instead.
	listened string
	// listening gets the listener once the server listens.
	listening chan net.Listener
	now       time.Time
	// onPort answers the CLI's requests to 127.0.0.1; nil is nothing
	// listening there.
	onPort http.Handler
	// agentPort is the only port onPort answers on, as runAgent found it in
	// config.json; empty is any port.
	agentPort string
	// otherPorts answers the CLI's requests to other ports, by port.
	otherPorts map[string]http.Handler
	// requested is the URL of each request the CLI made.
	requested []string
	// programs answers the programs that are installed, by name; any other
	// is not found, as exec reports it.
	programs map[string]func(args []string) ([]byte, error)
	// ran is each program run, with what it got on stdin.
	ran []ranProgram
	// deviceErr is what a write to a device file fails with, by path.
	deviceErr map[string]error
	// written is what each device file got.
	written map[string][]byte
	// spawned is each detached program started, name and arguments joined by
	// spaces; onSpawn is what starting one does.
	spawned []string
	onSpawn func()
	// terminated is each pid asked to exit; onTerminate is what that does, and
	// terminateErr what it fails with.
	terminated   []int
	onTerminate  func()
	terminateErr error
	// killed is each pid force-killed; onKill is what that does, and killErr
	// what it fails with.
	killed  []int
	onKill  func()
	killErr error
	// exes is the program file of each process that this user can see.
	exes map[int]string
	// runValues is the user's HKCU Run key; runValueErr is what changing it
	// fails with.
	runValues   map[string]string
	runValueErr error
	// stopRequests is what the agent is asked to stop through, and
	// stopRequestsErr why it cannot be.
	stopRequests    chan struct{}
	stopRequestsErr error
	// pathEnv is the PATH of this process; userPath is the user's Path in HKCU,
	// and userPathErr what changing it fails with.
	pathEnv     string
	userPath    string
	userPathErr error
	// installedPrograms is each file written as a program, runnable.
	installedPrograms []string
	// removeErr is what removing a file fails with, by its path.
	removeErr map[string]error
	// renameErr is what a rename to a path fails with once, by that path.
	renameErr map[string]error
	// redirects and downloads are the web, by URL: what a URL redirects to, and
	// what it gives. Any other URL is a 404. offline is what reaching the web
	// fails with, and downloadErr what only a download does.
	redirects   map[string]string
	downloads   map[string][]byte
	offline     error
	downloadErr error
	// fetched is each URL asked of the web.
	fetched []string
	// buildVersions is the version an agent reports, by what the installed binary
	// holds when it is started; "" is one that exits at once. Any other build is dev.
	buildVersions map[string]string
}

// ranProgram is one program the CLI ran.
type ranProgram struct {
	command string // the name and arguments, joined by spaces
	stdin   []byte
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{
		goos:       "linux",
		uid:        501,
		home:       "/home/me",
		configDir:  "/home/me/.config",
		executable: "/home/me/.local/bin/printpipe",
		files:      map[string][]byte{},
		listening:  make(chan net.Listener, 1),
		now:        time.Date(2026, 11, 3, 20, 15, 4, 0, time.UTC),
		programs:   map[string]func(args []string) ([]byte, error){},
		deviceErr:  map[string]error{},
		written:    map[string][]byte{},
		exes:       map[int]string{},
		runValues:  map[string]string{},
		pathEnv:    "/usr/bin:/home/me/.local/bin",
		renameErr:  map[string]error{},
	}
}

func (s *fakeSystem) Run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	s.ran = append(s.ran, ranProgram{strings.Join(append([]string{name}, args...), " "), stdin})
	program, ok := s.programs[name]
	if !ok {
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return program(args)
}

func (s *fakeSystem) WriteDevice(_ context.Context, path string, job []byte) error {
	if err := s.deviceErr[path]; err != nil {
		return err
	}
	s.written[path] = job
	return nil
}

// ReadDir is what the fake's files have directly under the folder.
func (s *fakeSystem) ReadDir(name string) ([]string, error) {
	var names []string
	for path := range s.files {
		if rest, ok := strings.CutPrefix(path, name+"/"); ok {
			entry, _, _ := strings.Cut(rest, "/")
			if !slices.Contains(names, entry) {
				names = append(names, entry)
			}
		}
	}
	if names == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	slices.Sort(names)
	return names, nil
}

func (s *fakeSystem) Redirect(_ context.Context, url string) (string, error) {
	s.fetched = append(s.fetched, url)
	if s.offline != nil {
		return "", s.offline
	}
	location, ok := s.redirects[url]
	if !ok {
		return "", httpStatus(http.StatusNotFound)
	}
	return location, nil
}

func (s *fakeSystem) Download(_ context.Context, url string) ([]byte, error) {
	s.fetched = append(s.fetched, url)
	if s.offline != nil {
		return nil, s.offline
	}
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	body, ok := s.downloads[url]
	if !ok {
		return nil, httpStatus(http.StatusNotFound)
	}
	return body, nil
}

func (s *fakeSystem) GOARCH() string                { return "amd64" }
func (s *fakeSystem) GOOS() string                  { return s.goos }
func (s *fakeSystem) UID() int                      { return s.uid }
func (s *fakeSystem) HomeDir() (string, error)      { return s.home, nil }
func (s *fakeSystem) ConfigDir() (string, error)    { return s.configDir, nil }
func (s *fakeSystem) LocalAppData() (string, error) { return s.home + `\AppData\Local`, nil }
func (s *fakeSystem) Executable() (string, error)   { return s.executable, nil }

func (s *fakeSystem) WriteFile(name string, data []byte) error {
	s.files[name] = data
	return nil
}

func (s *fakeSystem) WriteProgram(name string, data []byte) error {
	s.files[name] = data
	s.installedPrograms = append(s.installedPrograms, name)
	return nil
}

func (s *fakeSystem) PathEnv() string { return s.pathEnv }

func (s *fakeSystem) UserPath() (string, error) { return s.userPath, nil }

func (s *fakeSystem) SetUserPath(value string) error {
	if s.userPathErr != nil {
		return s.userPathErr
	}
	s.userPath = value
	return nil
}

func (s *fakeSystem) Remove(name string) error {
	if err := s.removeErr[name]; err != nil {
		return err
	}
	if _, ok := s.files[name]; !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}
	delete(s.files, name)
	return nil
}

func (s *fakeSystem) Rename(from, to string) error {
	b, ok := s.files[from]
	if !ok {
		return &fs.PathError{Op: "rename", Path: from, Err: fs.ErrNotExist}
	}
	if err := s.renameErr[to]; err != nil {
		delete(s.renameErr, to)
		return err
	}
	s.files[to] = b
	delete(s.files, from)
	// A running program goes on running under its file's new name.
	for pid, exe := range s.exes {
		if exe == from {
			s.exes[pid] = to
		}
	}
	return nil
}

func (s *fakeSystem) OpenAppend(name string) (io.WriteCloser, int64, error) {
	return fakeAppender{s, name}, int64(len(s.files[name])), nil
}

// fakeAppender adds what is written to a file of the fake.
type fakeAppender struct {
	s    *fakeSystem
	name string
}

func (a fakeAppender) Write(p []byte) (int, error) {
	a.s.files[a.name] = append(a.s.files[a.name], p...)
	return len(p), nil
}

func (a fakeAppender) Close() error { return nil }

func (s *fakeSystem) Spawn(name string, args ...string) error {
	s.spawned = append(s.spawned, strings.Join(append([]string{name}, args...), " "))
	if s.onSpawn != nil {
		s.onSpawn()
	}
	return nil
}

func (s *fakeSystem) Terminate(pid int) error {
	if s.terminateErr != nil {
		return s.terminateErr
	}
	s.terminated = append(s.terminated, pid)
	if s.onTerminate != nil {
		s.onTerminate()
	}
	return nil
}

func (s *fakeSystem) Kill(pid int) error {
	if s.killErr != nil {
		return s.killErr
	}
	s.killed = append(s.killed, pid)
	if s.onKill != nil {
		s.onKill()
	}
	return nil
}

func (s *fakeSystem) ProcessExe(pid int) (string, error) {
	exe, ok := s.exes[pid]
	if !ok {
		return "", fs.ErrPermission
	}
	return exe, nil
}

func (s *fakeSystem) StopRequests() (<-chan struct{}, error) {
	return s.stopRequests, s.stopRequestsErr
}

func (s *fakeSystem) HasRunValue(name string) bool {
	_, ok := s.runValues[name]
	return ok
}

func (s *fakeSystem) SetRunValue(name, command string) error {
	if s.runValueErr != nil {
		return s.runValueErr
	}
	s.runValues[name] = command
	return nil
}

func (s *fakeSystem) DeleteRunValue(name string) error {
	if s.runValueErr != nil {
		return s.runValueErr
	}
	if _, ok := s.runValues[name]; !ok {
		return &fs.PathError{Op: "delete", Path: name, Err: fs.ErrNotExist}
	}
	delete(s.runValues, name)
	return nil
}

// Sleep moves the clock on, so that a test waiting for the agent ends at once.
func (s *fakeSystem) Sleep(d time.Duration) { s.now = s.now.Add(d) }

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
	port := req.URL.Port()
	if other, ok := s.otherPorts[port]; ok {
		rec := httptest.NewRecorder()
		other.ServeHTTP(rec, req)
		return rec.Result(), nil
	}
	if s.onPort == nil || (s.agentPort != "" && s.agentPort != port) {
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

// runAgent makes a printpipe agent answer on the port config.json names now,
// and keep it until it runs again, printing to p. It allows origins, or without
// any those config.json names, as a real agent does.
func (s *fakeSystem) runAgent(p *fakePrinter, origins ...string) {
	s.agentPort = strconv.Itoa(config.DefaultPort)
	version := "dev"
	if files, err := filesOf(s); err == nil {
		if v, ok := s.buildVersions[string(s.files[files.Binary])]; ok {
			if v == "" {
				s.onPort = nil
				return
			}
			version = v
		}
		if cfg, err := config.Parse(s.files[files.Config]); err == nil {
			s.agentPort = strconv.Itoa(cfg.Port)
			if len(origins) == 0 {
				origins = cfg.Origins
			}
		}
	}
	s.onPort = server.Handler(server.Options{Version: version, PID: 4242, Origins: origins}, p)
}

// runForeignProgram makes another program answer on the port.
func (s *fakeSystem) runForeignProgram() {
	s.onPort = foreignProgram
}

var foreignProgram = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "<html>hello</html>")
})
