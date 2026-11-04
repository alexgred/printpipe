package cli

import (
	"context"
	"encoding/json"
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
// client's wait for one that is printing.
const healthTimeout = 2 * time.Second

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
	if e.sys.GOOS() != "linux" {
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
	if agent, err = waitForAgent(e, cfg.Port); err != nil {
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

// terminate asks the agent with this pid to exit, once its program file shows
// that it is printpipe: the pid comes from whatever answered on the port.
func terminate(e env, port, pid int) error {
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
	if exe != own && exe != files.Binary {
		return &failure{
			msg:   fmt.Sprintf("process %d on port %d runs %s, not this printpipe", pid, port, exe),
			hints: []string{"Close that program and run the same command again."},
		}
	}
	return e.sys.Terminate(pid)
}

// replaceByHand ends the agent with this pid and starts svc's in its place.
func replaceByHand(e env, svc service, port, pid int) error {
	if err := terminate(e, port, pid); err != nil {
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
	if err := e.sys.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
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
