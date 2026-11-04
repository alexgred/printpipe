package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	unitPath      = "/home/me/.config/systemd/user/printpipe.service"
	autostartPath = "/home/me/.config/autostart/printpipe.desktop"
	installedExe  = "/home/me/.local/bin/printpipe"
	agentPID      = 4242
)

// fakeSystemd is systemctl --user with a user manager: it runs the agent as
// the unit, which it must have been given first.
type fakeSystemd struct {
	sys     *fakeSystem
	enabled bool
	// mainPID is the agent the unit runs, 0 for none.
	mainPID int
}

func withSystemd(sys *fakeSystem) *fakeSystemd {
	d := &fakeSystemd{sys: sys}
	sys.programs["systemctl"] = d.systemctl
	return d
}

func (d *fakeSystemd) systemctl(args []string) ([]byte, error) {
	if args[0] != "--user" {
		return nil, fmt.Errorf("not the user's manager: %v", args)
	}
	_, hasUnit := d.sys.files[unitPath]
	switch args[1] {
	case "show-environment", "daemon-reload":
		return nil, nil
	case "show":
		return fmt.Appendf(nil, "%d\n", d.mainPID), nil
	case "is-enabled":
		if d.enabled {
			return []byte("enabled\n"), nil
		}
		return []byte("disabled\n"), errors.New("systemctl: exit status 1")
	}
	if !hasUnit {
		return nil, errors.New("systemctl: exit status 5: Unit printpipe.service not found.")
	}
	switch args[1] {
	case "start", "restart":
		d.sys.runAgent(&fakePrinter{})
		d.sys.exes[agentPID] = installedExe
		d.mainPID = agentPID
	case "stop":
		d.sys.onPort = nil
		d.mainPID = 0
	case "enable":
		d.enabled = true
	case "disable":
		d.enabled = false
	default:
		return nil, fmt.Errorf("unknown systemctl command %v", args)
	}
	return nil, nil
}

// runUnit makes the agent run as the unit.
func (d *fakeSystemd) runUnit() {
	d.sys.runAgent(&fakePrinter{})
	d.sys.exes[agentPID] = installedExe
	d.sys.files[unitPath] = []byte("[Service]\n")
	d.mainPID = agentPID
}

// ranCommands is each program the CLI ran.
func (s *fakeSystem) ranCommands() []string {
	var commands []string
	for _, r := range s.ran {
		commands = append(commands, r.command)
	}
	return commands
}

func wantRan(t *testing.T, sys *fakeSystem, command string) {
	t.Helper()
	if !slices.Contains(sys.ranCommands(), command) {
		t.Errorf("did not run %q: ran %q", command, sys.ranCommands())
	}
}

func wantNotRan(t *testing.T, sys *fakeSystem, command string) {
	t.Helper()
	if slices.Contains(sys.ranCommands(), command) {
		t.Errorf("ran %q", command)
	}
}

func wantOutput(t *testing.T, got result, stdout string) {
	t.Helper()
	if got.code != 0 || got.stdout != stdout || got.stderr != "" {
		t.Errorf("got %+v, want exit 0 and %q", got, stdout)
	}
}

func wantFailure(t *testing.T, got result, stderr string) {
	t.Helper()
	if got.code != 1 || got.stderr != stderr {
		t.Errorf("got %+v, want exit 1 and %q", got, stderr)
	}
}

func TestStartWritesTheUnitAndStartsIt(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
	wantRan(t, sys, "systemctl --user start printpipe.service")

	unit := string(sys.files[unitPath])
	for _, want := range []string{`ExecStart="` + installedExe + `" serve`, "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("the unit has no %q:\n%s", want, unit)
		}
	}
}

// What start runs is the program that was started, wherever it is.
func TestTheUnitRunsThisProgramEvenInAFolderWithASpace(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	sys.executable = "/home/me/my tools/printpipe"
	run(t, sys, "start")

	if unit := string(sys.files[unitPath]); !strings.Contains(unit, `ExecStart="/home/me/my tools/printpipe" serve`) {
		t.Errorf("the unit does not run the program:\n%s", unit)
	}
}

// A changed unit does not take effect until the manager reads it again.
func TestAChangedUnitIsReloaded(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	sys.files[unitPath] = []byte("an older unit")
	run(t, sys, "start")
	wantRan(t, sys, "systemctl --user daemon-reload")

	sys.ran, sys.onPort = nil, nil
	run(t, sys, "start")
	wantNotRan(t, sys, "systemctl --user daemon-reload")
}

func TestStartOnARunningAgentJustSaysSo(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys).runUnit()

	wantOutput(t, run(t, sys, "start"), "already running, version dev\n")
	wantNotRan(t, sys, "systemctl --user start printpipe.service")
}

// Another user's agent holds the port, so this user's agent cannot run: not
// the "already running" that means this user can print.
func TestStartReportsAnotherUsersAgentWithItsPID(t *testing.T) {
	sys := noManager()
	sys.runAgent(&fakePrinter{}) // a process this user may not look at

	wantFailure(t, run(t, sys, "start"), "error: could not check process 4242 on port 8631: permission denied\n"+
		"  It may be another user's agent.\n")
	if len(sys.spawned) != 0 {
		t.Errorf("spawned %q", sys.spawned)
	}
}

func TestStartWhenTheAgentDoesNotAnswerPointsAtTheLog(t *testing.T) {
	sys := newFakeSystem()
	sys.programs["systemctl"] = func([]string) ([]byte, error) { return nil, nil }

	wantFailure(t, run(t, sys, "start"), "error: the agent did not answer within 10 s\n  Log: "+logPath+"\n")
}

func TestStartReportsAManagerThatRefuses(t *testing.T) {
	sys := newFakeSystem()
	sys.programs["systemctl"] = func(args []string) ([]byte, error) {
		if args[1] == "start" {
			return nil, errors.New("systemctl: exit status 1: Access denied")
		}
		return nil, nil
	}

	wantFailure(t, run(t, sys, "start"), "error: systemctl: exit status 1: Access denied\n")
}

func TestStopStopsTheUnit(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys).runUnit()

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	wantRan(t, sys, "systemctl --user stop printpipe.service")
}

func TestStopOnAStoppedAgentJustSaysSo(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)

	wantOutput(t, run(t, sys, "stop"), "not running\n")
}

func TestRestartRestartsTheUnit(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys).runUnit()

	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	wantRan(t, sys, "systemctl --user restart printpipe.service")
}

func TestRestartOnAStoppedAgentStartsIt(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)

	wantOutput(t, run(t, sys, "restart"), "Agent started, version dev\n")
}

func TestAnotherProgramOnThePortIsNeverTouched(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart"} {
		t.Run(command, func(t *testing.T) {
			sys := newFakeSystem()
			withSystemd(sys)
			sys.files[configPath] = []byte(`{"port": 9100}`)
			sys.runForeignProgram()

			wantFailure(t, run(t, sys, command), "error: port 9100 is used by another program\n"+
				"  Close that program and run the same command again.\n"+
				"  Windows: netstat -ano | findstr :9100   macOS/Linux: lsof -i :9100\n")
			for _, ran := range sys.ranCommands() {
				if ran != "systemctl --user show-environment" {
					t.Errorf("ran %q", ran)
				}
			}
			if len(sys.terminated)+len(sys.spawned) != 0 {
				t.Errorf("terminated %v, spawned %v", sys.terminated, sys.spawned)
			}
		})
	}
}

// startup and start are independent: neither turns the other on or off.
func TestStartupTurnsTheUnitOnAndOffWithoutTouchingTheAgent(t *testing.T) {
	sys := newFakeSystem()
	d := withSystemd(sys)

	wantOutput(t, run(t, sys, "startup"), "Starts at login: off\n")
	wantOutput(t, run(t, sys, "startup", "on"), "Starts at login: on\n")
	wantRan(t, sys, "systemctl --user enable printpipe.service")
	if sys.onPort != nil {
		t.Error("startup on started the agent")
	}
	wantOutput(t, run(t, sys, "startup"), "Starts at login: on\n")

	d.runUnit()
	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
	wantRan(t, sys, "systemctl --user disable printpipe.service")
	if sys.onPort == nil {
		t.Error("startup off stopped the agent")
	}
}

func TestStartupOffOnAFreshComputerSucceeds(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)

	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
}

func TestStartupRefusesAnythingButOnOrOff(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		error string
	}{
		{[]string{"startup", "maybe"}, `error: startup takes on or off, not "maybe"`},
		{[]string{"startup", "on", "off"}, `error: unexpected argument "off"`},
		{[]string{"start", "now"}, `error: unexpected argument "now"`},
	} {
		got := run(t, newFakeSystem(), tc.args...)
		if got.code != 2 || !strings.HasPrefix(got.stderr, tc.error+"\n") {
			t.Errorf("%q: got %+v, want exit 2 and %q", tc.args, got, tc.error)
		}
	}
}

// macOS has no service wired in so far.
func TestMacOSSaysItIsNotThereYet(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart", "startup"} {
		sys := newFakeSystem()
		sys.goos = "darwin"
		wantFailure(t, run(t, sys, command), "error: printpipe "+command+" is not implemented yet on this OS\n")
	}
}

// A manager that exists can still not own the agent: printpipe serve in a
// terminal, or a child started when there was no manager yet.
func TestStopStopsAnAgentTheUnitDoesNotRun(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	runStrayAgent(sys)

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
}

func TestRestartHandsAnAgentTheUnitDoesNotRunOverToTheUnit(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	runStrayAgent(sys)

	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
	wantRan(t, sys, "systemctl --user start printpipe.service")
}

// runStrayAgent runs the agent as a process of this printpipe that exits on
// SIGTERM.
func runStrayAgent(sys *fakeSystem) {
	sys.runAgent(&fakePrinter{})
	sys.exes[agentPID] = installedExe
	sys.onTerminate = func() { sys.onPort = nil }
}

// noManager is a computer with no user manager: systemctl is there, but
// cannot reach one.
func noManager() *fakeSystem {
	sys := newFakeSystem()
	sys.programs["systemctl"] = func([]string) ([]byte, error) {
		return nil, errors.New("systemctl: exit status 1: Failed to connect to bus: No medium found")
	}
	sys.onSpawn = func() { runStrayAgent(sys) }
	return sys
}

func TestWithoutAManagerStartSpawnsADetachedChild(t *testing.T) {
	sys := noManager()

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
	if !slices.Equal(sys.spawned, []string{installedExe + " serve"}) {
		t.Errorf("spawned %q", sys.spawned)
	}
	if _, wrote := sys.files[unitPath]; wrote {
		t.Error("wrote a unit with no manager to read it")
	}
}

func TestWithoutSystemctlStartSpawnsADetachedChild(t *testing.T) {
	sys := noManager()
	delete(sys.programs, "systemctl")

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
}

func TestWithoutAManagerStartOnARunningAgentJustSaysSo(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)

	wantOutput(t, run(t, sys, "start"), "already running, version dev\n")
	if len(sys.spawned) != 0 {
		t.Errorf("spawned %q", sys.spawned)
	}
}

func TestWithoutAManagerStartPointsAtTheLogWhenTheChildDoesNotAnswer(t *testing.T) {
	sys := noManager()
	sys.onSpawn = nil

	wantFailure(t, run(t, sys, "start"), "error: the agent did not answer within 10 s\n  Log: "+logPath+"\n")
}

func TestWithoutAManagerStopSendsSIGTERMToTheAgentOnThePort(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
}

// A copy that is not installed stops the agent it started.
func TestStopAcceptsTheAgentOfThisCopy(t *testing.T) {
	sys := noManager()
	sys.executable = "/home/me/Downloads/printpipe"
	runStrayAgent(sys)
	sys.exes[agentPID] = sys.executable

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
}

// /health only says what the answering program claims: its pid is signalled
// only once the process turns out to run printpipe.
func TestStopNeverSignalsAProcessThatIsNotPrintpipe(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)
	sys.exes[agentPID] = "/usr/bin/firefox"

	wantFailure(t, run(t, sys, "stop"), "error: process 4242 on port 8631 runs /usr/bin/firefox, not this printpipe\n"+
		"  Close that program and run the same command again.\n")
	if len(sys.terminated) != 0 {
		t.Errorf("terminated %v", sys.terminated)
	}
}

func TestStopReportsAnAgentItCannotCheckWithItsPID(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)
	delete(sys.exes, agentPID)

	wantFailure(t, run(t, sys, "stop"), "error: could not check process 4242 on port 8631: permission denied\n"+
		"  It may be another user's agent.\n")
	if len(sys.terminated) != 0 {
		t.Errorf("terminated %v", sys.terminated)
	}
}

func TestStopSaysSoWhenTheAgentKeepsRunning(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)
	sys.onTerminate = nil

	wantFailure(t, run(t, sys, "stop"), "error: the agent (pid 4242) did not stop within 10 s\n")
}

func TestWithoutAManagerRestartStopsAndSpawns(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)

	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) || len(sys.spawned) != 1 {
		t.Errorf("terminated %v, spawned %q", sys.terminated, sys.spawned)
	}
}

func TestWithoutAManagerRestartOnAStoppedAgentStartsIt(t *testing.T) {
	sys := noManager()

	wantOutput(t, run(t, sys, "restart"), "Agent started, version dev\n")
}

func TestWithoutAManagerStartupIsAnXDGAutostartEntry(t *testing.T) {
	sys := noManager()

	wantOutput(t, run(t, sys, "startup"), "Starts at login: off\n")
	wantOutput(t, run(t, sys, "startup", "on"), "Starts at login: on\n")
	entry := string(sys.files[autostartPath])
	for _, want := range []string{"[Desktop Entry]\n", "Type=Application\n", `Exec="` + installedExe + `" start` + "\n"} {
		if !strings.Contains(entry, want) {
			t.Errorf("the entry has no %q:\n%s", want, entry)
		}
	}
	if len(sys.spawned) != 0 {
		t.Error("startup on started the agent")
	}
	wantOutput(t, run(t, sys, "startup"), "Starts at login: on\n")

	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
	if _, left := sys.files[autostartPath]; left {
		t.Error("the entry is still there")
	}
	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
}

// An entry written while there was no manager still starts the agent at login
// once there is one, so it counts, and off removes it.
func TestWithAManagerStartupAlsoSeesAnEarlierAutostartEntry(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	sys.files[autostartPath] = []byte("[Desktop Entry]\n")

	wantOutput(t, run(t, sys, "startup"), "Starts at login: on\n")
	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
	if _, left := sys.files[autostartPath]; left {
		t.Error("the entry is still there")
	}
}

const windowsExe = `C:\Users\me\AppData\Local\Programs\printpipe\printpipe.exe`

// onWindows is a Windows PC whose agent, once spawned, exits when asked to.
func onWindows() *fakeSystem {
	sys := newFakeSystem()
	sys.goos = "windows"
	sys.home = `C:\Users\me`
	sys.configDir = `C:\Users\me\AppData\Roaming`
	sys.executable = windowsExe
	sys.onSpawn = func() { runWindowsAgent(sys) }
	return sys
}

// runWindowsAgent runs the agent as a process of the installed printpipe, which
// exits when told to through its stop event.
func runWindowsAgent(sys *fakeSystem) {
	sys.runAgent(&fakePrinter{})
	sys.exes[agentPID] = windowsExe
	sys.onTerminate = func() { sys.onPort = nil }
}

func TestOnWindowsStartSpawnsTheHiddenAgent(t *testing.T) {
	sys := onWindows()

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
	if !slices.Equal(sys.spawned, []string{windowsExe + " serve"}) {
		t.Errorf("spawned %q", sys.spawned)
	}
	if len(sys.ran) != 0 {
		t.Errorf("ran %q", sys.ranCommands())
	}
}

func TestOnWindowsStartOnARunningAgentJustSaysSo(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)

	wantOutput(t, run(t, sys, "start"), "already running, version dev\n")
	if len(sys.spawned) != 0 {
		t.Errorf("spawned %q", sys.spawned)
	}
}

func TestOnWindowsStartPointsAtTheLogWhenTheAgentDoesNotAnswer(t *testing.T) {
	sys := onWindows()
	sys.onSpawn = nil

	wantFailure(t, run(t, sys, "start"), "error: the agent did not answer within 10 s\n"+
		`  Log: C:\Users\me\AppData\Local\printpipe\agent.log`+"\n")
}

func TestOnWindowsStopAsksTheAgentThroughItsEventAndKillsNothing(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("asked %v to exit, want the agent", sys.terminated)
	}
	if len(sys.killed) != 0 {
		t.Errorf("killed %v", sys.killed)
	}
}

func TestOnWindowsStopKillsAnAgentThatIgnoresItsEvent(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)
	sys.onTerminate = nil
	sys.onKill = func() { sys.onPort = nil }

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	if !slices.Equal(sys.killed, []int{agentPID}) {
		t.Errorf("killed %v, want the agent", sys.killed)
	}
}

// No event to signal is an agent that could not make one: it would never exit
// by itself, so there is nothing to wait for.
func TestOnWindowsStopKillsAtOnceAnAgentWithNoEvent(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)
	sys.terminateErr = errors.New("The system cannot find the file specified.")
	sys.onKill = func() { sys.onPort = nil }
	before := sys.now

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	if !slices.Equal(sys.killed, []int{agentPID}) {
		t.Errorf("killed %v, want the agent", sys.killed)
	}
	if !sys.now.Equal(before) {
		t.Errorf("waited %v before the kill", sys.now.Sub(before))
	}
}

func TestOnWindowsStopSaysSoWhenTheAgentSurvivesTheKill(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)
	sys.onTerminate = nil

	wantFailure(t, run(t, sys, "stop"), "error: the agent (pid 4242) did not stop within 10 s\n")
}

func TestOnWindowsStopReportsAKillThatFails(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)
	sys.onTerminate = nil
	sys.killErr = errors.New("Access is denied.")

	wantFailure(t, run(t, sys, "stop"), "error: Access is denied.\n")
}

// /health only says what the answering program claims.
func TestOnWindowsStopNeverTouchesAProcessThatIsNotPrintpipe(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)
	sys.exes[agentPID] = `C:\Program Files\Other\other.exe`

	wantFailure(t, run(t, sys, "stop"), `error: process 4242 on port 8631 runs C:\Program Files\Other\other.exe, not this printpipe`+"\n"+
		"  Close that program and run the same command again.\n")
	if len(sys.terminated)+len(sys.killed) != 0 {
		t.Errorf("asked %v to exit, killed %v", sys.terminated, sys.killed)
	}
}

// The wait for the event is long enough for another program to have taken the
// port's pid: the check is made again right before the kill.
func TestOnWindowsStopChecksTheProgramAgainBeforeTheKill(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)
	sys.onTerminate = func() { sys.exes[agentPID] = `C:\Program Files\Other\other.exe` }

	got := run(t, sys, "stop")
	if got.code != 1 || !strings.Contains(got.stderr, "not this printpipe") {
		t.Errorf("got %+v, want it refused", got)
	}
	if len(sys.killed) != 0 {
		t.Errorf("killed %v", sys.killed)
	}
}

// Windows' file names do not care about case, and the two ways of asking for
// a program's path need not agree on it.
func TestOnWindowsTheProgramFileIsComparedWithoutCase(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)
	sys.exes[agentPID] = strings.ToUpper(windowsExe)

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
}

func TestOnLinuxTheProgramFileIsComparedWithCase(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)
	sys.exes[agentPID] = strings.ToUpper(installedExe)

	got := run(t, sys, "stop")
	if got.code != 1 || !strings.Contains(got.stderr, "not this printpipe") {
		t.Errorf("got %+v, want it refused", got)
	}
}

func TestOnWindowsStopReportsAnotherUsersAgentWithItsPID(t *testing.T) {
	sys := onWindows()
	sys.runAgent(&fakePrinter{}) // a process this user may not look at

	wantFailure(t, run(t, sys, "stop"), "error: could not check process 4242 on port 8631: permission denied\n"+
		"  It may be another user's agent.\n")
}

func TestOnWindowsStopOnAStoppedAgentJustSaysSo(t *testing.T) {
	wantOutput(t, run(t, onWindows(), "stop"), "not running\n")
}

func TestOnWindowsRestartStopsAndSpawns(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)

	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) || len(sys.spawned) != 1 {
		t.Errorf("asked %v to exit, spawned %q", sys.terminated, sys.spawned)
	}
}

func TestOnWindowsRestartOnAStoppedAgentStartsIt(t *testing.T) {
	sys := onWindows()

	wantOutput(t, run(t, sys, "restart"), "Agent started, version dev\n")
	if len(sys.spawned) != 1 {
		t.Errorf("spawned %q", sys.spawned)
	}
}

// startup and start are independent: neither turns the other on or off.
func TestOnWindowsStartupIsARunValueThatStartsTheAgent(t *testing.T) {
	sys := onWindows()

	wantOutput(t, run(t, sys, "startup"), "Starts at login: off\n")
	wantOutput(t, run(t, sys, "startup", "on"), "Starts at login: on\n")
	if got, want := sys.runValues["printpipe"], `"`+windowsExe+`" start`; got != want {
		t.Errorf("the value runs %q, want %q", got, want)
	}
	if len(sys.spawned) != 0 {
		t.Error("startup on started the agent")
	}
	wantOutput(t, run(t, sys, "startup"), "Starts at login: on\n")

	runWindowsAgent(sys)
	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
	if _, left := sys.runValues["printpipe"]; left {
		t.Error("the value is still there")
	}
	if sys.onPort == nil || len(sys.terminated)+len(sys.killed) != 0 {
		t.Error("startup off stopped the agent")
	}
	wantOutput(t, run(t, sys, "startup"), "Starts at login: off\n")
}

func TestOnWindowsStartupOffWithNoValueSucceeds(t *testing.T) {
	wantOutput(t, run(t, onWindows(), "startup", "off"), "Starts at login: off\n")
}

func TestOnWindowsStartupReportsARegistryThatRefuses(t *testing.T) {
	for _, state := range []string{"on", "off"} {
		sys := onWindows()
		sys.runValues["printpipe"] = "something"
		sys.runValueErr = errors.New("Access is denied.")

		wantFailure(t, run(t, sys, "startup", state), "error: Access is denied.\n")
	}
}
