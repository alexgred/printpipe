package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/alexgred/printpipe/internal/config"
)

// runningAgent is what the agent on the port says of itself.
type runningAgent struct {
	Name    string
	Version string
	PID     int
}

// healthTimeout is far above a healthy agent's answer, and well below the
// client's wait for one that is printing. It is also well above the 2 s or so
// that Windows takes to refuse a connection to a closed port: cut short, that
// would look like a program on the port.
const healthTimeout = 5 * time.Second

// findAgent asks the port who answers, the only way the agent is ever found.
// It is nil when nothing listens, and another program answering is an error,
// so that nothing is ever sent to, or stopped of, a program printpipe does not
// own.
func findAgent(e env, port int) (*runningAgent, error) {
	ctx, cancel := context.WithTimeout(e.ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL(port)+"/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.sys.Do(req)
	if e.ctx.Err() != nil {
		return nil, e.ctx.Err()
	}
	var dial *net.OpError
	if errors.As(err, &dial) && dial.Op == "dial" {
		return nil, nil
	}
	if err != nil {
		return nil, portBusy(port)
	}
	defer resp.Body.Close()
	var agent runningAgent
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&agent) != nil || agent.Name != "printpipe" {
		return nil, portBusy(port)
	}
	return &agent, nil
}

func agentURL(port int) string { return "http://" + agentAddr(port) }

func portBusy(port int) error {
	return &failure{
		msg: fmt.Sprintf("port %d is used by another program", port),
		hints: []string{
			"Close that program and run the same command again.",
			fmt.Sprintf("Windows: netstat -ano | findstr :%d   macOS/Linux: lsof -i :%d", port, port),
		},
	}
}

// service is how this computer keeps the agent running in the background.
// None of its methods waits for the agent to answer; the commands do that.
type service interface {
	// start starts a stopped agent.
	start(e env) error
	// stop stops the running agent with this pid.
	stop(e env, port, pid int) error
	// stopStarting keeps an agent that does not answer from coming up later.
	stopStarting(e env) error
	// restart replaces the running agent with this pid.
	restart(e env, port, pid int) error
	// setStartup turns starting at login on or off, leaving the agent as it is.
	setStartup(e env, on bool) error
	// startsAtLogin is whether starting at login is on.
	startsAtLogin(e env) bool
}

// agentWaitSeconds is how long a command waits for the agent to come up or go.
const agentWaitSeconds = 10

// serviceOf is the service of this computer.
func serviceOf(e env, command string) (service, error) {
	switch e.sys.GOOS() {
	case "linux":
	case "windows":
		return windows{}, nil
	case "darwin":
		return launchd{}, nil
	default:
		return nil, &failure{msg: fmt.Sprintf("printpipe %s is not implemented yet on this OS", command)}
	}
	if _, err := e.sys.Run(e.ctx, nil, "systemctl", "--user", "show-environment"); err == nil {
		return systemd{}, nil
	}
	return detached{}, nil
}

// findBackground is what start, stop and restart begin with: the service, the
// configuration and the agent running now, nil for none. Another program on
// the port is an error.
func findBackground(e env, command string) (service, config.Config, *runningAgent, error) {
	svc, err := serviceOf(e, command)
	if err != nil {
		return nil, config.Config{}, nil, err
	}
	cfg, _, err := loadConfig(e, "", false)
	if err != nil {
		return nil, config.Config{}, nil, err
	}
	agent, err := findAgent(e, cfg.Port)
	return svc, cfg, agent, err
}

func start(e env) error {
	svc, cfg, agent, err := findBackground(e, "start")
	if err != nil {
		return err
	}
	if agent != nil {
		if _, err := agentExe(e, cfg.Port, agent.PID); err != nil {
			return err
		}
		return e.say("already running, version %s", agent.Version)
	}
	return startAgent(e, svc, cfg)
}

func startAgent(e env, svc service, cfg config.Config) error {
	if err := svc.start(e); err != nil {
		return err
	}
	agent, err := waitForAgent(e, cfg.Port)
	if err != nil {
		return err
	}
	return e.say("Agent started, version %s", agent.Version)
}

func stop(e env) error {
	svc, cfg, agent, err := findBackground(e, "stop")
	if err != nil {
		return err
	}
	if agent == nil {
		if err := svc.stopStarting(e); err != nil {
			return err
		}
		return e.say("not running")
	}
	if err := svc.stop(e, cfg.Port, agent.PID); err != nil {
		return err
	}
	if err := waitForAgentGone(e, cfg.Port); err != nil {
		return err
	}
	return e.say("Agent stopped")
}

func restart(e env) error {
	svc, cfg, agent, err := findBackground(e, "restart")
	if err != nil {
		return err
	}
	if agent == nil {
		return startAgent(e, svc, cfg)
	}
	if err := svc.restart(e, cfg.Port, agent.PID); err != nil {
		return err
	}
	return sayRestarted(e, cfg.Port)
}

// sayRestarted waits for the agent that replaced the old one, on its port.
func sayRestarted(e env, port int) error {
	agent, err := waitForAgent(e, port)
	if err != nil {
		return err
	}
	return e.say("Agent restarted, version %s", agent.Version)
}

func startup(e env) error {
	if len(e.args) == 1 && e.args[0] != "on" && e.args[0] != "off" {
		return usageError(fmt.Sprintf("startup takes on or off, not %q", e.args[0]))
	}
	svc, err := serviceOf(e, "startup")
	if err != nil {
		return err
	}
	var on bool
	if len(e.args) == 0 {
		on = svc.startsAtLogin(e)
	} else {
		on = e.args[0] == "on"
		if err := svc.setStartup(e, on); err != nil {
			return err
		}
	}
	state := "off"
	if on {
		state = "on"
	}
	return e.say("Starts at login: %s", state)
}

// waitForAgent waits for the agent to answer on the port.
func waitForAgent(e env, port int) (*runningAgent, error) {
	agent, err := pollAgent(e, port, func(a *runningAgent) bool { return a != nil })
	if err != nil || agent != nil {
		return agent, err
	}
	files, err := filesOf(e.sys)
	if err != nil {
		return nil, err
	}
	return nil, &failure{msg: fmt.Sprintf("the agent did not answer within %d s", agentWaitSeconds), hints: []string{"Log: " + files.Log}}
}

// waitForAgentGone waits for nothing to answer on the port.
func waitForAgentGone(e env, port int) error {
	agent, err := pollAgent(e, port, func(a *runningAgent) bool { return a == nil })
	if err != nil || agent == nil {
		return err
	}
	return &failure{msg: fmt.Sprintf("the agent (pid %d) did not stop within %d s", agent.PID, agentWaitSeconds)}
}

// pollAgent asks the port until done accepts what it finds, or the wait is
// over, and returns the last it found.
func pollAgent(e env, port int, done func(*runningAgent) bool) (*runningAgent, error) {
	deadline := e.sys.Now().Add(agentWaitSeconds * time.Second)
	for {
		agent, err := findAgent(e, port)
		if err != nil || done(agent) || !e.sys.Now().Before(deadline) {
			return agent, err
		}
		e.sys.Sleep(100 * time.Millisecond)
	}
}

// agentExe is the program file of the process that answered on the port. Not
// being able to see it is how another user's agent shows.
func agentExe(e env, port, pid int) (string, error) {
	exe, err := e.sys.ProcessExe(pid)
	if err != nil {
		return "", &failure{
			msg:   fmt.Sprintf("could not check process %d on port %d: %v", pid, port, err),
			hints: []string{"It may be another user's agent."},
		}
	}
	return exe, nil
}

// checkOwnAgent fails unless the process with this pid runs printpipe, by its
// program file: the pid comes from whatever answered on the port.
func checkOwnAgent(e env, port, pid int) error {
	exe, err := agentExe(e, port, pid)
	if err != nil {
		return err
	}
	own, err := e.sys.Executable()
	if err != nil {
		return err
	}
	files, err := filesOf(e.sys)
	if err != nil {
		return err
	}
	same := func(a, b string) bool { return a == b }
	if e.sys.GOOS() == "windows" {
		same = strings.EqualFold
	}
	if !same(exe, own) && !same(exe, files.Binary) {
		return &failure{
			msg:   fmt.Sprintf("process %d on port %d runs %s, not this printpipe", pid, port, exe),
			hints: []string{"Close that program and run the same command again."},
		}
	}
	return nil
}

// terminate asks the agent with this pid to exit, once it shows to be printpipe.
func terminate(e env, port, pid int) error {
	if err := checkOwnAgent(e, port, pid); err != nil {
		return err
	}
	return e.sys.Terminate(pid)
}

// replaceByHand stops the agent with this pid and starts svc's in its place.
func replaceByHand(e env, svc service, port, pid int) error {
	if err := svc.stop(e, port, pid); err != nil {
		return err
	}
	if err := waitForAgentGone(e, port); err != nil {
		return err
	}
	return svc.start(e)
}

// quotedExe is a program path as both a systemd unit and an XDG desktop entry
// read it: a space stays inside the quotes and a % is not a specifier. A path
// with a ", \ or $ would need each format's own escaping, and is not handled.
func quotedExe(exe string) string {
	return `"` + strings.ReplaceAll(exe, "%", "%%") + `"`
}

// autostartFile is the XDG autostart entry, used without a user manager.
func autostartFile(sys System) (string, error) {
	dir, err := sys.ConfigDir()
	if err != nil {
		return "", err
	}
	return path.Join(dir, "autostart", "printpipe.desktop"), nil
}

// removeAutostart removes the XDG autostart entry, if there is one.
func removeAutostart(e env) error {
	file, err := autostartFile(e.sys)
	if err != nil {
		return err
	}
	return removeIfThere(e.sys, file)
}

// removeIfThere removes a file; one that is not there is no error.
func removeIfThere(sys System, file string) error {
	if err := sys.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// hasAutostart is whether the XDG autostart entry exists.
func hasAutostart(e env) bool {
	file, err := autostartFile(e.sys)
	if err != nil {
		return false
	}
	_, err = e.sys.ReadFile(file)
	return err == nil
}

// systemd is the user's systemd manager, which owns the agent as a unit.
type systemd struct{}

const unitName = "printpipe.service"

func (systemd) systemctl(e env, args ...string) ([]byte, error) {
	return e.sys.Run(e.ctx, nil, "systemctl", append([]string{"--user"}, args...)...)
}

// writeUnit makes the unit run this program. The agent's output goes nowhere:
// it keeps its own log, not the journal. Exit 1 is a config it cannot work
// with or a taken port, and trying again every few seconds fixes neither.
func (d systemd) writeUnit(e env) error {
	exe, err := e.sys.Executable()
	if err != nil {
		return err
	}
	dir, err := e.sys.ConfigDir()
	if err != nil {
		return err
	}
	file := path.Join(dir, "systemd", "user", unitName)
	unit := "[Unit]\n" +
		"Description=printpipe print agent\n" +
		"\n" +
		"[Service]\n" +
		"ExecStart=" + quotedExe(exe) + " serve\n" +
		"Restart=on-failure\n" +
		"RestartPreventExitStatus=1\n" +
		"StandardOutput=null\n" +
		"StandardError=null\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
	if old, err := e.sys.ReadFile(file); err == nil && string(old) == unit {
		return nil
	}
	if err := e.sys.WriteFile(file, []byte(unit)); err != nil {
		return err
	}
	_, err = d.systemctl(e, "daemon-reload")
	return err
}

func (d systemd) start(e env) error {
	if err := d.writeUnit(e); err != nil {
		return err
	}
	_, err := d.systemctl(e, "start", unitName)
	return err
}

// runsAgent is whether the agent with this pid is the unit's. It may not be:
// printpipe serve in a terminal, or a child started when there was no manager.
func (d systemd) runsAgent(e env, pid int) (bool, error) {
	out, err := d.systemctl(e, "show", "-p", "MainPID", "--value", unitName)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == strconv.Itoa(pid), nil
}

func (d systemd) stop(e env, port, pid int) error {
	owned, err := d.runsAgent(e, pid)
	if err != nil {
		return err
	}
	if !owned {
		return terminate(e, port, pid)
	}
	_, err = d.systemctl(e, "stop", unitName)
	return err
}

// A unit that exited with 1 stays down.
func (systemd) stopStarting(env) error { return nil }

// restart hands an agent that is not the unit's over to the unit.
func (d systemd) restart(e env, port, pid int) error {
	owned, err := d.runsAgent(e, pid)
	if err != nil {
		return err
	}
	if !owned {
		return replaceByHand(e, d, port, pid)
	}
	if err := d.writeUnit(e); err != nil {
		return err
	}
	_, err = d.systemctl(e, "restart", unitName)
	return err
}

func (d systemd) setStartup(e env, on bool) error {
	if err := d.writeUnit(e); err != nil {
		return err
	}
	if on {
		_, err := d.systemctl(e, "enable", unitName)
		return err
	}
	if _, err := d.systemctl(e, "disable", unitName); err != nil {
		return err
	}
	return removeAutostart(e)
}

// startsAtLogin also counts an autostart entry from when there was no manager:
// it still starts the agent at login. is-enabled fails when the unit is not
// enabled, saying so on stdout.
func (d systemd) startsAtLogin(e env) bool {
	out, _ := d.systemctl(e, "is-enabled", unitName)
	return strings.TrimSpace(string(out)) == "enabled" || hasAutostart(e)
}

// detached is a computer without a user manager: the agent is a child in its
// own session, found again by its port, and an XDG autostart entry starts it
// at login.
type detached struct{}

func (detached) start(e env) error {
	exe, err := e.sys.Executable()
	if err != nil {
		return err
	}
	return e.sys.Spawn(exe, "serve")
}

func (detached) stop(e env, port, pid int) error {
	return terminate(e, port, pid)
}

func (detached) stopStarting(env) error { return nil }

func (d detached) restart(e env, port, pid int) error {
	return replaceByHand(e, d, port, pid)
}

func (detached) setStartup(e env, on bool) error {
	if !on {
		return removeAutostart(e)
	}
	exe, err := e.sys.Executable()
	if err != nil {
		return err
	}
	file, err := autostartFile(e.sys)
	if err != nil {
		return err
	}
	return e.sys.WriteFile(file, []byte("[Desktop Entry]\n"+
		"Type=Application\n"+
		"Name=printpipe\n"+
		"Comment=printpipe print agent\n"+
		"Exec="+quotedExe(exe)+" start\n"+
		"NoDisplay=true\n"))
}

func (detached) startsAtLogin(e env) bool { return hasAutostart(e) }

// windows is a PC with Windows: the agent is a process with no console, found
// again by its port.
type windows struct{}

func (windows) start(e env) error { return detached{}.start(e) }

// stop asks the agent to exit through its event, then ends it by force. An
// agent that goes off its port has stopped, though it may finish a job first.
// With no event to signal, from another logon session or for an agent that
// could not make one, there is nothing to wait for. After a wait the process is
// checked again before the kill: the wait is long, and the port may have a new
// owner.
func (windows) stop(e env, port, pid int) error {
	if err := checkOwnAgent(e, port, pid); err != nil {
		return err
	}
	if e.sys.Terminate(pid) != nil {
		return e.sys.Kill(pid)
	}
	agent, err := pollAgent(e, port, func(a *runningAgent) bool { return a == nil })
	if err != nil || agent == nil {
		return err
	}
	if err := checkOwnAgent(e, port, agent.PID); err != nil {
		return err
	}
	return e.sys.Kill(agent.PID)
}

func (windows) stopStarting(env) error { return nil }

func (w windows) restart(e env, port, pid int) error {
	return replaceByHand(e, w, port, pid)
}

// runValue is the agent's value in the user's HKCU Run key.
const runValue = "printpipe"

func (windows) setStartup(e env, on bool) error {
	if !on {
		if err := e.sys.DeleteRunValue(runValue); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := e.sys.Executable()
	if err != nil {
		return err
	}
	return e.sys.SetRunValue(runValue, `"`+exe+`" start`)
}

func (windows) startsAtLogin(e env) bool { return e.sys.HasRunValue(runValue) }

// launchd is the Mac's user session manager, which owns the agent as a service.
type launchd struct{}

const launchdLabel = "io.github.alexgred.printpipe"

func (launchd) launchctl(e env, args ...string) ([]byte, error) {
	return e.sys.Run(e.ctx, nil, "launchctl", args...)
}

func (launchd) domain(e env) string { return "gui/" + strconv.Itoa(e.sys.UID()) }

func (l launchd) service(e env) string { return l.domain(e) + "/" + launchdLabel }

// plistFile is the plist launchd is given, in printpipe's Application Support
// folder.
func (launchd) plistFile(e env) (string, error) {
	dir, err := e.sys.ConfigDir()
	if err != nil {
		return "", err
	}
	return path.Join(dir, "printpipe", launchdLabel+".plist"), nil
}

// plist makes the service run this program. The agent's output goes nowhere: it
// keeps its own log. launchd cannot tell one failing exit from another, so it
// also runs again, every 10 s, an agent that exits with 1; what stops that is
// taking the service out of launchd, which stop does.
func (launchd) plist(e env) ([]byte, error) {
	exe, err := e.sys.Executable()
	if err != nil {
		return nil, err
	}
	var program bytes.Buffer
	if err := xml.EscapeText(&program, []byte(exe)); err != nil {
		return nil, err
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + program.String() + `</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
</dict>
</plist>
`), nil
}

// loginPlist is the copy in LaunchAgents, which launchd loads at login. It is
// there only while starting at login is on.
func (launchd) loginPlist(e env) (string, error) {
	home, err := e.sys.HomeDir()
	if err != nil {
		return "", err
	}
	return path.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

// writePlist writes the plist, and returns it and whether it changed. A copy
// in LaunchAgents follows, so that login runs the same program as start.
func (l launchd) writePlist(e env) ([]byte, bool, error) {
	plist, err := l.plist(e)
	if err != nil {
		return nil, false, err
	}
	file, err := l.plistFile(e)
	if err != nil {
		return nil, false, err
	}
	old, err := e.sys.ReadFile(file)
	changed := err != nil || !bytes.Equal(old, plist)
	if changed {
		if err := e.sys.WriteFile(file, plist); err != nil {
			return nil, false, err
		}
	}
	if l.startsAtLogin(e) {
		login, err := l.loginPlist(e)
		if err != nil {
			return nil, false, err
		}
		if err := e.sys.WriteFile(login, plist); err != nil {
			return nil, false, err
		}
	}
	return plist, changed, nil
}

// loadedService is what launchd has of the service: pid is the agent it runs,
// 0 for none, and program what it read from the plist.
type loadedService struct {
	loaded  bool
	pid     int
	program string
}

// loadedService asks launchd, whose print fails when it does not have the
// service. A line it does not recognize leaves its field empty.
func (l launchd) loadedService(e env) loadedService {
	out, err := l.launchctl(e, "print", l.service(e))
	if err != nil {
		return loadedService{}
	}
	found := loadedService{loaded: true}
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "pid = "); ok {
			found.pid, _ = strconv.Atoi(value)
		}
		if value, ok := strings.CutPrefix(line, "program = "); ok {
			found.program = value
		}
	}
	return found
}

// start also serves a service launchd already has, which it would refuse to
// bootstrap: an agent that exited leaves it loaded. launchd reads a plist only
// when it loads it, so one that changed, or another program than it loaded,
// takes loading again. kickstart -k is for the same program.
func (l launchd) start(e env) error {
	_, changed, err := l.writePlist(e)
	if err != nil {
		return err
	}
	exe, err := e.sys.Executable()
	if err != nil {
		return err
	}
	service := l.loadedService(e)
	if service.loaded && !changed && service.program == exe {
		_, err := l.launchctl(e, "kickstart", "-k", l.service(e))
		return err
	}
	if err := l.stopStarting(e); err != nil {
		return err
	}
	file, err := l.plistFile(e)
	if err != nil {
		return err
	}
	_, err = l.launchctl(e, "bootstrap", l.domain(e), file)
	return err
}

// stop ends an agent launchd does not run by itself: printpipe serve in a
// terminal. A service that is loaded all the same goes too, or it would take
// the port once that is free.
func (l launchd) stop(e env, port, pid int) error {
	if l.loadedService(e).pid != pid {
		if err := terminate(e, port, pid); err != nil {
			return err
		}
	}
	return l.stopStarting(e)
}

// stopStarting takes the service out of launchd, which ends its agent: launchd
// would run again one that exits with 1.
func (l launchd) stopStarting(e env) error {
	if !l.loadedService(e).loaded {
		return nil
	}
	file, err := l.plistFile(e)
	if err != nil {
		return err
	}
	_, err = l.launchctl(e, "bootout", l.domain(e), file)
	return err
}

// restart hands an agent launchd does not run over to the service.
func (l launchd) restart(e env, port, pid int) error {
	if l.loadedService(e).pid != pid {
		return replaceByHand(e, l, port, pid)
	}
	return l.start(e)
}

// setStartup only moves the copy in LaunchAgents, and never runs launchctl
// disable: that would outlast the copy and keep bootstrap from starting the
// agent.
func (l launchd) setStartup(e env, on bool) error {
	login, err := l.loginPlist(e)
	if err != nil {
		return err
	}
	if !on {
		return removeIfThere(e.sys, login)
	}
	plist, _, err := l.writePlist(e)
	if err != nil {
		return err
	}
	return e.sys.WriteFile(login, plist)
}

func (l launchd) startsAtLogin(e env) bool {
	login, err := l.loginPlist(e)
	if err != nil {
		return false
	}
	_, err = e.sys.ReadFile(login)
	return err == nil
}
