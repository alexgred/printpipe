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
	// printer is what the unit's agent prints to.
	printer *fakePrinter
}

func withSystemd(sys *fakeSystem) *fakeSystemd {
	d := &fakeSystemd{sys: sys, printer: &fakePrinter{}}
	sys.programs["systemctl"] = d.systemctl
	return d
}

func (d *fakeSystemd) systemctl(args []string) ([]byte, error) {
	if args[0] != "--user" {
		return nil, fmt.Errorf("not the user's manager: %v", args)
	}
	_, hasUnit := d.sys.files[unitPath]
	switch args[1] {
	case "show-environment", "daemon-reload", "reset-failed":
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
		d.sys.runAgent(d.printer)
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

// A downloaded copy may be gone by the next login: what the unit runs stays.
func TestTheUnitRunsTheInstalledProgramWhicheverCopyWroteIt(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	sys.executable = "/home/me/Downloads/printpipe"
	sys.files[installedExe] = []byte("installed")
	run(t, sys, "startup", "on")

	if unit := string(sys.files[unitPath]); !strings.Contains(unit, `ExecStart="`+installedExe+`" serve`) {
		t.Errorf("the unit does not run the installed program:\n%s", unit)
	}
}

func TestTheUnitRunsTheInstalledProgramEvenInAFolderWithASpace(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	sys.home = "/home/my self"
	sys.executable = "/home/my self/.local/bin/printpipe"
	run(t, sys, "start")

	if unit := string(sys.files[unitPath]); !strings.Contains(unit, `ExecStart="/home/my self/.local/bin/printpipe" serve`) {
		t.Errorf("the unit does not run the installed program:\n%s", unit)
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

// An OS with no service wired in says so.
func TestAnotherOSSaysItIsNotThereYet(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart", "startup"} {
		sys := newFakeSystem()
		sys.goos = "freebsd"
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
	sys.executable = "/home/me/Downloads/printpipe"
	sys.files[installedExe] = []byte("installed")
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
	sys.executable = `C:\Users\me\Downloads\printpipe.exe`
	sys.files[windowsExe] = []byte("installed")
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

const (
	macExe         = "/Users/me/.local/bin/printpipe"
	plistPath      = "/Users/me/Library/Application Support/printpipe/io.github.alexgred.printpipe.plist"
	loginPlistPath = "/Users/me/Library/LaunchAgents/io.github.alexgred.printpipe.plist"
	launchdDomain  = "gui/501"
	launchdService = "gui/501/io.github.alexgred.printpipe"
)

// fakeLaunchd is launchctl in the user's GUI session. As the real one, it
// refuses to load a service twice and to act on one that is not loaded.
type fakeLaunchd struct {
	sys    *fakeSystem
	loaded bool
	// pid is the agent the service runs, 0 for none.
	pid int
	// program is what launchd read from the plist when it loaded the service.
	program string
	// starts is whether a started service answers on the port.
	starts bool
}

// onMac is a Mac whose launchd runs the agent when it is told to.
func onMac() (*fakeSystem, *fakeLaunchd) {
	sys := newFakeSystem()
	sys.goos = "darwin"
	sys.home = "/Users/me"
	sys.configDir = "/Users/me/Library/Application Support"
	sys.executable = macExe
	l := &fakeLaunchd{sys: sys, starts: true}
	sys.programs["launchctl"] = l.launchctl
	return sys, l
}

func (l *fakeLaunchd) launchctl(args []string) ([]byte, error) {
	notLoaded := errors.New("launchctl: exit status 113: Could not find service")
	switch args[0] {
	case "print":
		if args[1] != launchdService {
			return nil, fmt.Errorf("not the service: %v", args)
		}
		if !l.loaded {
			return nil, notLoaded
		}
		out := launchdService + " = {\n\tactive count = 1\n\tprogram = " + l.program + "\n"
		if l.pid != 0 {
			out += fmt.Sprintf("\tstate = running\n\tpid = %d\n", l.pid)
		} else {
			out += "\tstate = not running\n"
		}
		return []byte(out + "}\n"), nil
	case "bootstrap":
		if args[1] != launchdDomain || args[2] != plistPath {
			return nil, fmt.Errorf("not the plist in the session: %v", args)
		}
		plist, ok := l.sys.files[plistPath]
		if !ok || l.loaded {
			return nil, errors.New("launchctl: exit status 5: Bootstrap failed: 5: Input/output error")
		}
		_, rest, _ := strings.Cut(string(plist), "<array>\n\t\t<string>")
		l.program, _, _ = strings.Cut(rest, "</string>")
		l.loaded = true
		l.run()
	case "bootout":
		if args[1] != launchdDomain || args[2] != plistPath {
			return nil, fmt.Errorf("not the plist in the session: %v", args)
		}
		if !l.loaded {
			return nil, errors.New("launchctl: exit status 3: Boot-out failed: 3: No such process")
		}
		l.loaded, l.pid = false, 0
		l.sys.onPort = nil
	case "kickstart":
		if args[1] != "-k" || args[2] != launchdService {
			return nil, fmt.Errorf("not a restart of the service: %v", args)
		}
		if !l.loaded {
			return nil, notLoaded
		}
		l.run()
	default:
		return nil, fmt.Errorf("unknown launchctl command %v", args)
	}
	return nil, nil
}

func (l *fakeLaunchd) run() {
	if !l.starts {
		return
	}
	l.sys.runAgent(&fakePrinter{})
	l.sys.exes[agentPID] = l.program
	l.pid = agentPID
}

// agentExits ends the service's agent and leaves the service loaded.
func (l *fakeLaunchd) agentExits() {
	l.pid, l.sys.onPort = 0, nil
}

// runService makes the agent run as the loaded service.
func (l *fakeLaunchd) runService() {
	l.loaded, l.program = true, macExe
	l.sys.files[plistPath] = plistOf(macExe)
	l.run()
}

// plistOf is the plist that runs this program, as start writes it.
func plistOf(exe string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.github.alexgred.printpipe</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + exe + `</string>
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
`)
}

func TestOnMacOSStartWritesThePlistAndBootstrapsIt(t *testing.T) {
	sys, _ := onMac()

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
	wantRan(t, sys, "launchctl bootstrap "+launchdDomain+" "+plistPath)
	if got, want := string(sys.files[plistPath]), string(plistOf(macExe)); got != want {
		t.Errorf("the plist is\n%s\nwant\n%s", got, want)
	}
	if _, wrote := sys.files[loginPlistPath]; wrote {
		t.Error("start turned starting at login on")
	}
}

// A path is text in XML: a folder with an & would make launchd refuse the plist.
func TestOnMacOSThePlistEscapesTheProgramPath(t *testing.T) {
	sys, _ := onMac()
	sys.home = "/Users/Tom & Jerry"
	sys.executable = "/Users/Tom & Jerry/.local/bin/printpipe"
	run(t, sys, "start")

	if plist := string(sys.files[plistPath]); !strings.Contains(plist, "<string>/Users/Tom &amp; Jerry/.local/bin/printpipe</string>") {
		t.Errorf("the path is not escaped:\n%s", plist)
	}
}

func TestOnMacOSStartOnARunningAgentJustSaysSo(t *testing.T) {
	sys, l := onMac()
	l.runService()

	wantOutput(t, run(t, sys, "start"), "already running, version dev\n")
	wantNotRan(t, sys, "launchctl bootstrap "+launchdDomain+" "+plistPath)
}

// The service stays loaded after an agent that exited, and launchd refuses to
// bootstrap it a second time.
func TestOnMacOSStartKickstartsALoadedServiceThatDoesNotAnswer(t *testing.T) {
	sys, l := onMac()
	l.runService()
	l.agentExits()

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
	wantRan(t, sys, "launchctl kickstart -k "+launchdService)
}

// launchd reads the plist only when it loads it.
func TestOnMacOSStartReloadsAServiceWhosePlistChanged(t *testing.T) {
	sys, l := onMac()
	l.runService()
	l.agentExits()
	sys.files[plistPath] = []byte("an older plist")

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
	wantRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
	wantRan(t, sys, "launchctl bootstrap "+launchdDomain+" "+plistPath)
}

func TestOnMacOSStartPointsAtTheLogWhenTheAgentDoesNotAnswer(t *testing.T) {
	sys, l := onMac()
	l.starts = false

	wantFailure(t, run(t, sys, "start"), "error: the agent did not answer within 10 s\n  Log: /Users/me/Library/Logs/printpipe.log\n")
}

func TestOnMacOSStopBootsTheServiceOut(t *testing.T) {
	sys, l := onMac()
	l.runService()

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	wantRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
	if len(sys.terminated) != 0 {
		t.Errorf("terminated %v", sys.terminated)
	}
}

func TestOnMacOSStopOnAStoppedAgentJustSaysSo(t *testing.T) {
	sys, _ := onMac()

	wantOutput(t, run(t, sys, "stop"), "not running\n")
	wantNotRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
}

// launchd keeps running again an agent that exits with 1, such as on a broken
// config: after stop it must not come up once the config is fixed.
func TestOnMacOSStopTakesOutAServiceWhoseAgentDoesNotAnswer(t *testing.T) {
	sys, l := onMac()
	l.runService()
	l.agentExits()

	wantOutput(t, run(t, sys, "stop"), "not running\n")
	if l.loaded {
		t.Error("the service is still loaded")
	}
}

// runStrayMacAgent runs the agent outside launchd, as printpipe serve in a
// terminal: a process of this printpipe that exits on SIGTERM.
func runStrayMacAgent(sys *fakeSystem) {
	runStrayAgent(sys)
	sys.exes[agentPID] = macExe
}

func TestOnMacOSStopStopsAnAgentLaunchdDoesNotRun(t *testing.T) {
	sys, _ := onMac()
	runStrayMacAgent(sys)

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
	wantNotRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
}

// The service keeps failing on the taken port, and would take it once free.
func TestOnMacOSStopAlsoTakesOutAServiceThatDoesNotRunTheAgent(t *testing.T) {
	sys, l := onMac()
	l.runService()
	l.pid = 0
	runStrayMacAgent(sys)

	wantOutput(t, run(t, sys, "stop"), "Agent stopped\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
	if l.loaded {
		t.Error("the service is still loaded")
	}
}

func TestOnMacOSStopNeverSignalsAProcessThatIsNotPrintpipe(t *testing.T) {
	sys, _ := onMac()
	runStrayMacAgent(sys)
	sys.exes[agentPID] = "/Applications/Other.app/Contents/MacOS/Other"

	wantFailure(t, run(t, sys, "stop"), "error: process 4242 on port 8631 runs /Applications/Other.app/Contents/MacOS/Other, not this printpipe\n"+
		"  Close that program and run the same command again.\n")
	if len(sys.terminated) != 0 {
		t.Errorf("terminated %v", sys.terminated)
	}
}

func TestOnMacOSRestartKickstartsTheService(t *testing.T) {
	sys, l := onMac()
	l.runService()

	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	wantRan(t, sys, "launchctl kickstart -k "+launchdService)
	wantNotRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
}

func TestOnMacOSRestartOnAStoppedAgentStartsIt(t *testing.T) {
	sys, _ := onMac()

	wantOutput(t, run(t, sys, "restart"), "Agent started, version dev\n")
	wantRan(t, sys, "launchctl bootstrap "+launchdDomain+" "+plistPath)
}

// As on Linux, what runs afterwards is the installed program, whichever copy
// was asked to restart.
func TestOnMacOSRestartReloadsAServiceWhosePlistChanged(t *testing.T) {
	sys, l := onMac()
	l.runService()
	sys.files[plistPath] = []byte("an older plist")
	sys.files[macExe] = []byte("installed")
	sys.executable = "/Users/me/Downloads/printpipe"

	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	wantRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
	wantRan(t, sys, "launchctl bootstrap "+launchdDomain+" "+plistPath)
	if plist := string(sys.files[plistPath]); !strings.Contains(plist, "<string>"+macExe+"</string>") {
		t.Errorf("the plist does not run the installed program:\n%s", plist)
	}
}

// The plist on disk says nothing of what launchd loaded: startup on has already
// written the new one, and kickstart would run the old program again.
func TestOnMacOSRestartAfterStartupMovesAServiceOffAnotherProgram(t *testing.T) {
	sys, l := onMac()
	l.runService()
	l.program = "/Users/me/Downloads/printpipe"

	run(t, sys, "startup", "on")
	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	if l.program != macExe {
		t.Errorf("launchd runs %q, want %q", l.program, macExe)
	}
}

func TestOnMacOSRestartHandsAnAgentLaunchdDoesNotRunOverToTheService(t *testing.T) {
	sys, l := onMac()
	runStrayMacAgent(sys)

	wantOutput(t, run(t, sys, "restart"), "Agent restarted, version dev\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
	if !l.loaded || l.pid != agentPID {
		t.Errorf("the service does not run the agent: loaded %v, pid %d", l.loaded, l.pid)
	}
}

// startup and start are independent: neither turns the other on or off, and
// launchctl plays no part, least of all launchctl disable.
func TestOnMacOSStartupCopiesThePlistToLaunchAgents(t *testing.T) {
	sys, _ := onMac()

	wantOutput(t, run(t, sys, "startup"), "Starts at login: off\n")
	wantOutput(t, run(t, sys, "startup", "on"), "Starts at login: on\n")
	if got, want := string(sys.files[loginPlistPath]), string(plistOf(macExe)); got != want {
		t.Errorf("LaunchAgents has\n%s\nwant\n%s", got, want)
	}
	if got, want := string(sys.files[plistPath]), string(plistOf(macExe)); got != want {
		t.Errorf("Application Support has\n%s\nwant\n%s", got, want)
	}
	wantOutput(t, run(t, sys, "startup"), "Starts at login: on\n")
	if sys.onPort != nil {
		t.Error("startup on started the agent")
	}

	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
	if _, left := sys.files[loginPlistPath]; left {
		t.Error("the copy is still in LaunchAgents")
	}
	if _, kept := sys.files[plistPath]; !kept {
		t.Error("the plist in Application Support is gone")
	}
	wantOutput(t, run(t, sys, "startup"), "Starts at login: off\n")
	if len(sys.ran) != 0 {
		t.Errorf("ran %q", sys.ranCommands())
	}
}

func TestOnMacOSStartupOffLeavesTheAgentRunning(t *testing.T) {
	sys, l := onMac()
	l.runService()
	sys.files[loginPlistPath] = plistOf(macExe)

	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
	if sys.onPort == nil || !l.loaded {
		t.Error("startup off stopped the agent")
	}
}

func TestOnMacOSStartupOffWithNoCopySucceeds(t *testing.T) {
	sys, _ := onMac()

	wantOutput(t, run(t, sys, "startup", "off"), "Starts at login: off\n")
}

// What starts at login is the same as what start runs, as with the Linux unit.
func TestOnMacOSAChangedPlistAlsoChangesTheCopyInLaunchAgents(t *testing.T) {
	sys, _ := onMac()
	run(t, sys, "startup", "on")
	sys.files[loginPlistPath] = []byte("an older plist")

	run(t, sys, "start")
	if got, want := string(sys.files[loginPlistPath]), string(plistOf(macExe)); got != want {
		t.Errorf("LaunchAgents has\n%s\nwant\n%s", got, want)
	}
}

// A changed plist does not turn starting at login on.
func TestOnMacOSAChangedPlistMakesNoCopyWhenStartupIsOff(t *testing.T) {
	sys, l := onMac()
	l.runService()
	sys.files[plistPath] = []byte("an older plist")

	run(t, sys, "restart")
	if _, wrote := sys.files[loginPlistPath]; wrote {
		t.Error("restart turned starting at login on")
	}
}
