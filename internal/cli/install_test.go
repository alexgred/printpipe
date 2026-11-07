//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/models"
	"github.com/alexgred/printpipe/internal/printer"
)

const downloadedExe = "/home/me/Downloads/printpipe"

// usableQueue is what lpoptions says of a queue for the XP-365B.
const usableQueue = "device-uri=" + xpURI + " printer-is-accepting-jobs=true"

// downloadedCopy makes the running printpipe a copy that is not installed.
func downloadedCopy(sys *fakeSystem) {
	sys.executable = downloadedExe
	sys.files[downloadedExe] = []byte("this build")
}

// runInstall is install up to its printer step's interest: an origin given, no
// test label, and an agent that comes up when it is started.
func runInstall(t *testing.T, sys *fakeSystem, args ...string) result {
	t.Helper()
	if sys.onSpawn == nil {
		sys.onSpawn = func() { runStrayAgent(sys) }
	}
	return run(t, sys, append([]string{"install", "--origin", app, "--no-test-print"}, args...)...)
}

func TestInstallTakesAComputerFromNothingToAPrintedTestLabel(t *testing.T) {
	sys := newFakeSystem()
	d := withSystemd(sys)
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	downloadedCopy(sys)

	got := run(t, sys, "install", "--origin", app)

	wantOutput(t, got, "Installed printpipe dev: "+installedExe+"\n"+
		"Printer: queue XP-365B\n"+
		"Config: "+configPath+"\n"+
		"Agent started, version dev\n"+
		"A test label should have come out of the printer\n"+
		"To start the agent at login: printpipe startup on\n")
	if string(sys.files[installedExe]) != "this build" {
		t.Errorf("the installed binary is %q", sys.files[installedExe])
	}
	if len(sys.installedPrograms) != 1 {
		t.Errorf("wrote as programs %q, want the binary, runnable", sys.installedPrograms)
	}
	if unit := string(sys.files[unitPath]); !strings.Contains(unit, `ExecStart="`+installedExe+`" serve`) {
		t.Errorf("the unit does not run the installed binary:\n%s", unit)
	}
	cfg := savedConfig(t, sys)
	if !slices.Equal(cfg.Origins, []string{app}) || cfg.Queue != "XP-365B" {
		t.Errorf("config has origins %q and queue %q", cfg.Origins, cfg.Queue)
	}
	if len(d.printer.jobs) != 1 {
		t.Errorf("the printer got %d jobs, want the test label", len(d.printer.jobs))
	}
	wantNotRan(t, sys, "systemctl --user enable printpipe.service")
}

// withXPQueue is a computer with a user manager and a queue for the XP-365B.
func withXPQueue() (*fakeSystem, *fakeSystemd) {
	sys := newFakeSystem()
	d := withSystemd(sys)
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	return sys, d
}

// A rerun is always a valid fix: nothing changes that was right, and the agent
// is restarted.
func TestInstallRunAgainFromTheInstalledBinaryRestartsAndPrintsAgain(t *testing.T) {
	sys, d := withXPQueue()
	downloadedCopy(sys)
	if got := run(t, sys, "install", "--origin", app); got.code != 0 {
		t.Fatalf("first run: %+v", got)
	}
	sys.executable = installedExe
	config := string(sys.files[configPath])

	got := run(t, sys, "install")

	wantOutput(t, got, "Installed printpipe dev: "+installedExe+"\n"+
		"Printer: queue XP-365B\n"+
		"Config: "+configPath+"\n"+
		"Agent restarted, version dev\n"+
		"A test label should have come out of the printer\n"+
		"To start the agent at login: printpipe startup on\n")
	wantRan(t, sys, "systemctl --user restart printpipe.service")
	if string(sys.files[configPath]) != config {
		t.Errorf("config.json changed:\n%s", sys.files[configPath])
	}
	if len(d.printer.jobs) != 2 {
		t.Errorf("the printer got %d jobs, want one per run", len(d.printer.jobs))
	}
	if len(sudoCommands(sys)) != 0 {
		t.Errorf("ran as root %q", sudoCommands(sys))
	}
}

// The same build run again from the download swaps nothing: the file of an agent
// that install failed to replace may be held where the swap moves the binary to.
func TestInstallRunAgainFromTheSameDownloadSwapsNothing(t *testing.T) {
	sys, _ := withXPQueue()
	downloadedCopy(sys)
	sys.files[installedExe] = []byte("this build")

	wantOutputCode(t, run(t, sys, "install", "--origin", app), 0)
	if len(sys.installedPrograms) != 0 {
		t.Errorf("wrote %q", sys.installedPrograms)
	}
}

func wantOutputCode(t *testing.T, got result, code int) {
	t.Helper()
	if got.code != code {
		t.Errorf("got %+v, want exit %d", got, code)
	}
}

// An older build is installed all the same: there is no downgrade argument.
func TestInstallReplacesADifferentBuildAndRemovesTheOldFile(t *testing.T) {
	sys, _ := withXPQueue()
	downloadedCopy(sys)
	sys.files[installedExe] = []byte("another build")
	sys.files["/home/me/.local/bin/.printpipe.new"] = []byte("left by a failed try")
	sys.files["/home/me/.local/bin/.printpipe.old"] = []byte("left by an earlier swap")

	wantOutputCode(t, run(t, sys, "install", "--origin", app), 0)

	if string(sys.files[installedExe]) != "this build" {
		t.Errorf("the installed binary is %q", sys.files[installedExe])
	}
	for _, left := range []string{"/home/me/.local/bin/.printpipe.new", "/home/me/.local/bin/.printpipe.old"} {
		if _, ok := sys.files[left]; ok {
			t.Errorf("%s is left", left)
		}
	}
}

func TestInstallThatCannotSwapLeavesTheInstalledBinary(t *testing.T) {
	for _, tc := range []struct{ name, failing string }{
		{"moving the old one away", "/home/me/.local/bin/.printpipe.old"},
		{"moving the new one in", installedExe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys, _ := withXPQueue()
			downloadedCopy(sys)
			sys.files[installedExe] = []byte("another build")
			sys.renameErr[tc.failing] = errors.New("Access is denied.")

			got := run(t, sys, "install", "--origin", app)

			wantFailure(t, got, "error: could not place printpipe in /home/me/.local/bin: Access is denied.\n"+
				"  If the agent is running, stop it and run the same command again: printpipe stop\n")
			if string(sys.files[installedExe]) != "another build" {
				t.Errorf("the installed binary is %q", sys.files[installedExe])
			}
			if _, left := sys.files["/home/me/.local/bin/.printpipe.new"]; left {
				t.Error("the new file is left")
			}
			if sys.onPort != nil {
				t.Error("the agent started")
			}
		})
	}
}

func TestInstallSaysHowToPutTheBinaryOnPathWithoutTouchingAProfile(t *testing.T) {
	sys, _ := withXPQueue()
	sys.pathEnv = "/usr/bin:/bin"

	got := run(t, sys, "install", "--origin", app)

	if want := `~/.local/bin is not on PATH; add this line to your shell profile: export PATH="$HOME/.local/bin:$PATH"` + "\n"; got.code != 0 || !strings.Contains(got.stdout, want) {
		t.Errorf("got %+v, want exit 0 and %q", got, want)
	}
}

func TestInstallSeesTheBinaryOnPathWithATrailingSlash(t *testing.T) {
	sys, _ := withXPQueue()
	sys.pathEnv = "/usr/bin:/home/me/.local/bin/"

	if got := run(t, sys, "install", "--origin", app); strings.Contains(got.stdout, "PATH") {
		t.Errorf("got %+v, want no PATH line", got)
	}
}

func TestInstallWithNoPrinterWritesTheConfigAndStartsNothing(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cups(sys, map[string]string{}, "")
	downloadedCopy(sys)

	got := run(t, sys, "install", "--origin", app, "--port", "9100")

	wantFailure(t, got, "error: no label printer queue and no known printer found\n"+
		"  Plug the XP-365B in, switch it on and run the same command again.\n"+
		"  An existing queue: printpipe config --queue <name>\n")
	if cfg := savedConfig(t, sys); !slices.Equal(cfg.Origins, []string{app}) || cfg.Port != 9100 {
		t.Errorf("config has origins %q and port %d, want them kept", cfg.Origins, cfg.Port)
	}
	if sys.onPort != nil {
		t.Error("the agent started")
	}
	wantNotRan(t, sys, "systemctl --user start printpipe.service")

	// The fix is to plug it in and run the same command.
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	if got := run(t, sys, "install", "--origin", app, "--port", "9100"); got.code != 0 {
		t.Errorf("the same command again: %+v", got)
	}
	if cfg := savedConfig(t, sys); len(cfg.Origins) != 1 {
		t.Errorf("origins %q, want one", cfg.Origins)
	}
}

// With a running agent on the old port and the new port in the file, no command
// could find the agent.
func TestInstallThatStopsLeavesARunningAgentsPortInTheFile(t *testing.T) {
	const newPort = "9100"
	busy := func(sys *fakeSystem) { sys.otherPorts = map[string]http.Handler{newPort: foreignProgram} }
	for _, tc := range []struct {
		name  string
		setup func(*fakeSystem)
	}{
		{"no printer", func(sys *fakeSystem) { cups(sys, map[string]string{}, "") }},
		{"the new port is taken", func(sys *fakeSystem) {
			cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
			busy(sys)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			d := withSystemd(sys)
			sys.files[configPath] = []byte(`{"origins": ["` + app + `"]}`)
			d.runUnit()
			tc.setup(sys)

			got := run(t, sys, "install", "--port", newPort)

			if got.code != 1 {
				t.Errorf("got %+v, want exit 1", got)
			}
			if cfg := savedConfig(t, sys); cfg.Port != 8631 {
				t.Errorf("config has port %d, want the agent's", cfg.Port)
			}
			wantNotRan(t, sys, "systemctl --user restart printpipe.service")
		})
	}
}

func TestInstallMovesARunningAgentToANewPort(t *testing.T) {
	sys, d := withXPQueue()
	sys.files[configPath] = []byte(`{"origins": ["` + app + `"]}`)
	d.runUnit()

	got := run(t, sys, "install", "--port", "9100")

	if got.code != 0 || !strings.Contains(got.stdout, "Agent restarted") {
		t.Errorf("got %+v, want exit 0 and the agent restarted", got)
	}
	if sys.agentPort != "9100" {
		t.Errorf("the agent listens on %s", sys.agentPort)
	}
}

func TestInstallWhenTheAgentDoesNotAnswerPointsAtTheLog(t *testing.T) {
	sys := newFakeSystem()
	sys.programs["systemctl"] = func([]string) ([]byte, error) { return nil, nil }
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")

	got := run(t, sys, "install", "--origin", app)

	wantFailure(t, got, "error: the agent did not answer within 10 s\n  Log: "+logPath+"\n")
}

// install knows no other print agent: whatever answers there is a foreign program.
func TestInstallLeavesAForeignProgramOnThePortAlone(t *testing.T) {
	sys, _ := withXPQueue()
	sys.runForeignProgram()

	got := run(t, sys, "install", "--origin", app)

	wantFailure(t, got, "error: port 8631 is used by another program\n"+
		"  Close that program and run the same command again.\n"+
		"  Windows: netstat -ano | findstr :8631   macOS/Linux: lsof -i :8631\n")
	wantNotRan(t, sys, "systemctl --user start printpipe.service")
	wantNotRan(t, sys, "systemctl --user restart printpipe.service")
	if len(sys.terminated)+len(sys.killed) != 0 {
		t.Errorf("terminated %v, killed %v", sys.terminated, sys.killed)
	}
	if cfg := savedConfig(t, sys); !slices.Equal(cfg.Origins, []string{app}) {
		t.Errorf("config has origins %q, want the origin kept", cfg.Origins)
	}
}

func TestInstallCanMoveOffAPortAForeignProgramHolds(t *testing.T) {
	sys, _ := withXPQueue()
	sys.runForeignProgram()
	sys.agentPort = "8631"

	got := run(t, sys, "install", "--origin", app, "--port", "9100")

	if got.code != 0 {
		t.Errorf("got %+v, want exit 0", got)
	}
}

func TestInstallWhoseTestLabelFailsLeavesTheAgentRunning(t *testing.T) {
	sys, d := withXPQueue()
	d.printer.err = fmt.Errorf("queue XP-365B: %w", printer.ErrNotResponding)

	got := run(t, sys, "install", "--origin", app)

	wantFailure(t, got, "error: the test label was not sent: the printer did not respond\n"+
		"  Check the paper, the cover and the power, then: printpipe test-print\n"+
		"  The agent is running.\n")
	if sys.onPort == nil {
		t.Error("the agent is not running")
	}
	wantNotRan(t, sys, "systemctl --user stop printpipe.service")
}

func TestInstallCanSkipTheTestLabelAndSaysSo(t *testing.T) {
	sys, d := withXPQueue()

	got := run(t, sys, "install", "--origin", app, "--no-test-print")

	if got.code != 0 || !strings.Contains(got.stdout, "Test label: skipped\n") || strings.Contains(got.stdout, "should have come out") {
		t.Errorf("got %+v, want exit 0 and the label skipped", got)
	}
	if len(d.printer.jobs) != 0 {
		t.Errorf("the printer got %d jobs", len(d.printer.jobs))
	}
}

func TestInstallMakesTheTestLabelForSize(t *testing.T) {
	sys, d := withXPQueue()

	if got := run(t, sys, "install", "--origin", app, "--size", "40,30"); got.code != 0 {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(string(d.printer.jobs[0]), "SIZE 40 mm, 30 mm") {
		t.Errorf("the job is %q", d.printer.jobs[0])
	}
}

func TestInstallSaysStartingAtLoginIsOnWhenItIs(t *testing.T) {
	sys, _ := withXPQueue()
	run(t, sys, "startup", "on")

	got := run(t, sys, "install", "--origin", app)

	if got.code != 0 || !strings.HasSuffix(got.stdout, "Starts at login: on\n") {
		t.Errorf("got %+v, want exit 0 and starting at login on", got)
	}
}

// install only starts; it never turns starting at login on.
func TestInstallLeavesStartingAtLoginOff(t *testing.T) {
	sys, _ := withXPQueue()
	run(t, sys, "install", "--origin", app)
	wantNotRan(t, sys, "systemctl --user enable printpipe.service")
}

// Wrong usage changes nothing.
func TestInstallWithWrongFlagsPlacesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no origin in the config", nil, "error: --origin is needed the first time: the page allowed to print, such as https://app.example\n"},
		{"not an origin", []string{"--origin", "https://app.example/"}, `is not an origin`},
		{"both printers", []string{"--origin", app, "--queue", "a", "--device", "/dev/usb/lp0"}, "use either --queue or --device, not both"},
		{"both printers with a usb device", []string{"--origin", app, "--queue", "a", "--device", "usb:1FC9:2016"}, "use either --queue or --device, not both"},
		{"a bad usb device", []string{"--origin", app, "--device", "usb:nope"}, `device "usb:nope"`},
		{"a bad port", []string{"--origin", app, "--port", "0"}, `--port "0" is not a port`},
		{"a bad size", []string{"--origin", app, "--size", "big"}, "--size "},
		{"a switch with a value", []string{"--origin", app, "--no-test-print=yes"}, "flag --no-test-print takes no value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys, _ := withXPQueue()
			downloadedCopy(sys)

			got := run(t, sys, append([]string{"install"}, tc.args...)...)

			if got.code != 2 || !strings.Contains(got.stderr, tc.want) {
				t.Errorf("got %+v, want exit 2 and %q", got, tc.want)
			}
			if len(sys.installedPrograms)+len(sys.ran) != 0 {
				t.Errorf("placed %q and ran %q", sys.installedPrograms, sys.ranCommands())
			}
			if _, wrote := sys.files[configPath]; wrote {
				t.Error("wrote config.json")
			}
		})
	}
}

func TestInstallAddsTheOriginToThoseThereOnce(t *testing.T) {
	sys, _ := withXPQueue()
	sys.files[configPath] = []byte(`{"origins": ["https://b.example"]}`)

	for range 2 {
		if got := run(t, sys, "install", "--origin", app); got.code != 0 {
			t.Fatalf("got %+v", got)
		}
	}
	if cfg := savedConfig(t, sys); !slices.Equal(cfg.Origins, []string{"https://b.example", app}) {
		t.Errorf("origins %q", cfg.Origins)
	}
}

// A device file is only ever set by a user, so one that is gone is no reason
// to print to another printer.
func TestInstallWithASavedDeviceFileThatIsGoneSaysSoAndStartsNothing(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	sys.files[configPath] = []byte(`{"origins": ["` + app + `"], "device": "/dev/usb/lp0"}`)

	got := run(t, sys, "install", "--no-test-print")

	wantFailure(t, got, "error: the device file /dev/usb/lp0 is not there\n"+
		"  Plug the printer in, switch it on and run the same command again.\n"+
		"  Another printer: printpipe config --device <file>, or --queue <name>\n")
	if cfg := savedConfig(t, sys); !slices.Equal(cfg.Origins, []string{app}) || cfg.Device != "/dev/usb/lp0" {
		t.Errorf("config has origins %q and device %q, want both kept", cfg.Origins, cfg.Device)
	}
	if sys.onPort != nil {
		t.Error("the agent started")
	}
	wantNotRan(t, sys, "systemctl --user start printpipe.service")

	// The fix is to plug it in and run the same command.
	sys.files["/dev/usb/lp0"] = nil
	sys.onSpawn = nil
	if got := runInstall(t, sys); got.code != 0 || !strings.Contains(got.stdout, "Printer: device /dev/usb/lp0\n") {
		t.Errorf("the same command again: %+v", got)
	}
}

func TestInstallWithAGivenDeviceFileThatIsNotThereSaysSo(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")

	got := runInstall(t, sys, "--device", "/dev/usb/lp1")

	wantFailure(t, got, "error: the device file /dev/usb/lp1 is not there\n"+
		"  Plug the printer in, switch it on and run the same command again.\n"+
		"  Another printer: printpipe config --device <file>, or --queue <name>\n")
	wantNotRan(t, sys, "systemctl --user start printpipe.service")
}

// A queue named on the command line is no reason to print to another one.
func TestInstallWithAQueueThatIsNotThereSaysSo(t *testing.T) {
	sys, _ := withXPQueue()

	got := run(t, sys, "install", "--origin", app, "--queue", "nope")

	wantFailure(t, got, "error: there is no printer queue nope\n"+
		"  See the queues: lpstat -e\n"+
		"  Run the same command again with one of them.\n")
	if sys.onPort != nil {
		t.Error("the agent started")
	}
}

func TestInstallFindsAnotherQueueWhenTheSavedOneIsGone(t *testing.T) {
	sys, _ := withXPQueue()
	sys.files[configPath] = []byte(`{"origins": ["` + app + `"], "queue": "gone"}`)

	got := run(t, sys, "install")

	if got.code != 0 || !strings.Contains(got.stdout, "Printer: queue XP-365B\n") {
		t.Errorf("got %+v, want exit 0 and the queue that is there", got)
	}
}

func TestInstallRefreshesAnEntryThatStartsAtLoginWithoutAManager(t *testing.T) {
	sys := noManager()
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	downloadedCopy(sys)
	sys.files[autostartPath] = []byte("[Desktop Entry]\nExec=\"/tmp/gone\" start\n")

	got := run(t, sys, "install", "--origin", app)

	if got.code != 0 || !strings.HasSuffix(got.stdout, "Starts at login: on\n") {
		t.Errorf("got %+v", got)
	}
	if entry := string(sys.files[autostartPath]); !strings.Contains(entry, `Exec="`+installedExe+`" start`) {
		t.Errorf("the entry runs:\n%s", entry)
	}
	if !slices.Equal(sys.spawned, []string{installedExe + " serve"}) {
		t.Errorf("spawned %q, want the installed binary", sys.spawned)
	}
}

func TestInstallWithoutAnEntryWritesNone(t *testing.T) {
	sys := noManager()
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")

	run(t, sys, "install", "--origin", app)

	if _, wrote := sys.files[autostartPath]; wrote {
		t.Error("install turned starting at login on")
	}
}

// Replacing the binary under a running child leaves its process with the name the
// file was moved to, and it is still printpipe's to stop.
func TestInstallRestartsAnAgentWhoseBinaryItJustMoved(t *testing.T) {
	sys := noManager()
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	sys.files[configPath] = []byte(`{"origins": ["` + app + `"]}`)
	sys.files[installedExe] = []byte("another build")
	runStrayAgent(sys)
	downloadedCopy(sys)

	got := run(t, sys, "install")

	if got.code != 0 || !strings.Contains(got.stdout, "Agent restarted") {
		t.Errorf("got %+v, want exit 0 and the agent restarted", got)
	}
	if !slices.Equal(sys.terminated, []int{agentPID}) || !slices.Equal(sys.spawned, []string{installedExe + " serve"}) {
		t.Errorf("terminated %v, spawned %q", sys.terminated, sys.spawned)
	}
}

// Windows.

const windowsFolder = `C:\Users\me\AppData\Local\Programs\printpipe`

// xpOnWindows is an XP-365B plugged in. ListUSB is the OS's own on the test's OS,
// the sysfs of a Linux computer here.
func xpOnWindows(sys *fakeSystem) {
	cupsComputer(sys, map[string]string{}, "")
	delete(sys.programs, "sudo")
}

func downloadedOnWindows(sys *fakeSystem) {
	sys.executable = `C:\Users\me\Downloads\printpipe-windows-amd64.exe`
	sys.files[sys.executable] = []byte("this build")
}

func TestOnWindowsInstallPlacesItselfAddsThePathAndFindsTheDevice(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	downloadedOnWindows(sys)
	sys.userPath = `%USERPROFILE%\bin;C:\tools;`

	got := run(t, sys, "install", "--origin", app)

	wantOutput(t, got, "Installed printpipe dev: "+windowsExe+"\n"+
		"Added to your Path; a new terminal will find printpipe by name\n"+
		"Printer: XP-365B (usb:1FC9:2016)\n"+
		`Config: C:\Users\me\AppData\Roaming\printpipe\config.json`+"\n"+
		"Agent started, version dev\n"+
		"A test label should have come out of the printer\n"+
		"To start the agent at login: printpipe startup on\n")
	if string(sys.files[windowsExe]) != "this build" {
		t.Errorf("the installed binary is %q", sys.files[windowsExe])
	}
	if want := `%USERPROFILE%\bin;C:\tools;` + windowsFolder; sys.userPath != want {
		t.Errorf("the user's Path is %q, want %q", sys.userPath, want)
	}
	if !slices.Equal(sys.spawned, []string{windowsExe + " serve"}) {
		t.Errorf("spawned %q, want the installed binary", sys.spawned)
	}
	if cfg := savedConfig(t, sys); cfg.Device != "usb:1FC9:2016" || cfg.Queue != "" {
		t.Errorf("config has device %q and queue %q, want the device without its serial", cfg.Device, cfg.Queue)
	}
}

func TestOnWindowsInstallRerunFindsThePathThereWhateverItsCase(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	path := `C:\tools;` + strings.ToUpper(windowsFolder) + `\`
	sys.userPath = path

	got := run(t, sys, "install", "--origin", app)

	if got.code != 0 || strings.Contains(got.stdout, "Path") || sys.userPath != path {
		t.Errorf("got %+v, the user's Path %q", got, sys.userPath)
	}
}

func TestOnWindowsInstallIsFineWithNoPathYet(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)

	run(t, sys, "install", "--origin", app)

	if sys.userPath != windowsFolder {
		t.Errorf("the user's Path is %q", sys.userPath)
	}
}

func TestOnWindowsInstallReportsAPathItCannotChange(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	sys.userPathErr = errors.New("Access is denied.")

	wantFailure(t, run(t, sys, "install", "--origin", app), "error: Access is denied.\n")
}

// A browser's mark makes Windows ask at logon, before the agent can start.
func TestOnWindowsInstallTakesTheDownloadMarkOffTheInstalledFile(t *testing.T) {
	const mark = windowsExe + ":Zone.Identifier"
	sys := onWindows()
	xpOnWindows(sys)
	sys.files[mark] = []byte("[ZoneTransfer]\nZoneId=3\n")

	if got := run(t, sys, "install", "--origin", app); got.code != 0 {
		t.Fatalf("got %+v", got)
	}
	if _, left := sys.files[mark]; left {
		t.Error("the installed file keeps its mark")
	}

	// A file with no mark is no error.
	if got := run(t, sys, "install", "--origin", app); got.code != 0 {
		t.Errorf("run again: %+v", got)
	}
}

// Nothing has verified a copy that is only run: its mark is not printpipe's to take.
func TestOnWindowsStartupLeavesTheMarkOfACopyAlone(t *testing.T) {
	sys := onWindows()
	downloadedOnWindows(sys)
	sys.files[windowsExe] = []byte("installed")
	mark := sys.executable + ":Zone.Identifier"
	sys.files[mark] = []byte("[ZoneTransfer]\nZoneId=3\n")

	wantOutput(t, run(t, sys, "startup", "on"), "Starts at login: on\n")
	if _, kept := sys.files[mark]; !kept {
		t.Error("startup took the mark off")
	}
}

func TestOnWindowsInstallRefreshesTheRunValueOnlyWhenItIsOn(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	downloadedOnWindows(sys)
	run(t, sys, "install", "--origin", app)
	if _, wrote := sys.runValues["printpipe"]; wrote {
		t.Error("install turned starting at login on")
	}

	sys.runValues["printpipe"] = `"C:\Users\me\Downloads\printpipe-windows-amd64.exe" start`
	got := run(t, sys, "install")

	if want := `"` + windowsExe + `" start`; sys.runValues["printpipe"] != want {
		t.Errorf("the value runs %q, want %q", sys.runValues["printpipe"], want)
	}
	if got.code != 0 || !strings.HasSuffix(got.stdout, "Starts at login: on\n") {
		t.Errorf("got %+v", got)
	}
}

func TestOnWindowsInstallRestartsAnAgentRunningFromTheFileItMoved(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	sys.files[windowsExe] = []byte("another build")
	runWindowsAgent(sys)
	downloadedOnWindows(sys)

	got := run(t, sys, "install", "--origin", app)

	if got.code != 0 || !strings.Contains(got.stdout, "Agent restarted") {
		t.Errorf("got %+v, want exit 0 and the agent restarted", got)
	}
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("asked %v to exit", sys.terminated)
	}
}

func TestOnWindowsInstallKeepsASavedDeviceOrQueueThatIsThere(t *testing.T) {
	for _, tc := range []struct{ name, config, line string }{
		{"a device with its serial", `"device": "usb:1FC9:2016:0020426981AA"`, "Printer: XP-365B (usb:1FC9:2016:0020426981AA)\n"},
		{"a queue", `"queue": "Office-label"`, "Printer: queue Office-label\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := onWindows()
			xpOnWindows(sys)
			windowsQueues(sys, "Office-label")
			files, _ := filesOf(sys)
			sys.files[files.Config] = []byte(`{"origins": ["` + app + `"], ` + tc.config + `}`)

			got := run(t, sys, "install")

			if got.code != 0 || !strings.Contains(got.stdout, tc.line) {
				t.Errorf("got %+v, want exit 0 and %q", got, tc.line)
			}
		})
	}
}

// A queue is never found by printpipe, only named by a user, so one that is
// gone is no reason to print to another printer. QueueExists asks CUPS on the
// test's OS, so lpstat stands in for the spooler here.
func TestOnWindowsInstallWithASavedQueueThatIsGoneSaysSoAndStartsNothing(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	windowsQueues(sys, "Other")
	files, _ := filesOf(sys)
	sys.files[files.Config] = []byte(`{"origins": ["` + app + `"], "queue": "Office-label"}`)

	got := run(t, sys, "install")

	wantFailure(t, got, "error: the printer queue Office-label is not there\n"+
		"  Plug the printer in, switch it on and run the same command again.\n"+
		"  Another printer: printpipe config --device usb:VID:PID, or --queue <name>\n")
	if len(sys.spawned) != 0 {
		t.Errorf("spawned %q", sys.spawned)
	}
	if cfg, _ := config.Parse(sys.files[files.Config]); !slices.Equal(cfg.Origins, []string{app}) || cfg.Queue != "Office-label" {
		t.Errorf("config has origins %q and queue %q, want both kept", cfg.Origins, cfg.Queue)
	}
}

func TestOnWindowsInstallThatCannotCheckTheQueueSaysWhy(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	sys.programs["lpstat"] = func([]string) ([]byte, error) { return nil, errors.New("the spooler is down") }

	got := run(t, sys, "install", "--origin", app, "--queue", "Office-label")

	wantFailure(t, got, "error: could not check the printer queue Office-label: the spooler is down\n")
	if len(sys.spawned) != 0 {
		t.Errorf("spawned %q", sys.spawned)
	}
}

// windowsQueues is the printers Windows has, as lpstat lists them.
func windowsQueues(sys *fakeSystem, names ...string) {
	sys.programs["lpstat"] = func(args []string) ([]byte, error) {
		if args[0] == "-r" {
			return []byte("scheduler is running\n"), nil
		}
		return []byte(strings.Join(names, "\n")), nil
	}
}

func TestOnWindowsInstallReplacesASavedDeviceThatIsGone(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	files, _ := filesOf(sys)
	sys.files[files.Config] = []byte(`{"origins": ["` + app + `"], "device": "usb:04B8:0202"}`)

	got := run(t, sys, "install")

	if got.code != 0 || !strings.Contains(got.stdout, "Printer: XP-365B (usb:1FC9:2016)\n") {
		t.Errorf("got %+v, want exit 0 and the printer that is there", got)
	}
}

func TestOnWindowsInstallWithADeviceThatIsNotConnectedSaysSo(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)

	got := run(t, sys, "install", "--origin", app, "--device", "usb:1FC9:2016:OTHER")

	wantFailure(t, got, "error: the printer usb:1FC9:2016:OTHER is not connected\n"+
		"  Plug it in, switch it on and run the same command again.\n")
	if sys.onPort != nil {
		t.Error("the agent started")
	}
}

func TestOnWindowsInstallWithNoKnownPrinterListsWhatItSees(t *testing.T) {
	const d = "/sys/bus/usb/devices/"
	const plug = "  Plug the XP-365B in, switch it on and run the same command again.\n  Another model: printpipe config --device usb:VID:PID\n"
	t.Run("nothing", func(t *testing.T) {
		sys := onWindows()
		got := run(t, sys, "install", "--origin", app)
		wantFailure(t, got, "error: no known label printer is connected\n  No USB printers found.\n"+plug)
		if len(sys.spawned) != 0 {
			t.Errorf("spawned %q", sys.spawned)
		}
		files, _ := filesOf(sys)
		if cfg, _ := config.Parse(sys.files[files.Config]); !slices.Equal(cfg.Origins, []string{app}) {
			t.Errorf("config has origins %q, want the origin kept", cfg.Origins)
		}
	})
	t.Run("another model", func(t *testing.T) {
		sys := onWindows()
		sys.files[d+"1-9/idVendor"] = []byte("04b8")
		sys.files[d+"1-9/idProduct"] = []byte("0202")
		sys.files[d+"1-9/manufacturer"] = []byte("EPSON")
		sys.files[d+"1-9/product"] = []byte("TM")
		sys.files[d+"1-9:1.0/bInterfaceClass"] = []byte("07")
		got := run(t, sys, "install", "--origin", app)
		wantFailure(t, got, "error: no known label printer is connected\n  USB printers found: 04B8:0202 \"EPSON TM\"\n"+plug)
	})
}

func TestOnWindowsInstallWithTwoOfTheModelGivesACommandPerDevice(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	secondXP(sys, "SECOND")

	got := run(t, sys, "install", "--origin", app)

	wantFailure(t, got, "error: 2 XP-365B printers are connected\n"+
		"  printpipe config --device usb:1FC9:2016:0020426981AA\n"+
		"  printpipe config --device usb:1FC9:2016:SECOND\n")
}

// Printers of a model printpipe does not know, matching the device saved or
// named, get a command each all the same.
func TestOnWindowsInstallWithTwoOfAnUnknownModelGivesACommandPerDevice(t *testing.T) {
	const want = "error: 2 printers match usb:0483:5720\n" +
		"  printpipe config --device usb:0483:5720:AAA\n" +
		"  printpipe config --device usb:0483:5720:BBB\n"
	t.Run("saved", func(t *testing.T) {
		sys := onWindows()
		plugUSB(sys, "1-8", "0483", "5720", "AAA")
		plugUSB(sys, "1-9", "0483", "5720", "BBB")
		files, _ := filesOf(sys)
		sys.files[files.Config] = []byte(`{"origins": ["` + app + `"], "device": "usb:0483:5720"}`)

		got := run(t, sys, "install")

		wantFailure(t, got, want)
		if len(sys.spawned) != 0 {
			t.Errorf("spawned %q", sys.spawned)
		}
	})
	t.Run("named", func(t *testing.T) {
		sys := onWindows()
		plugUSB(sys, "1-8", "0483", "5720", "AAA")
		plugUSB(sys, "1-9", "0483", "5720", "BBB")

		got := run(t, sys, "install", "--origin", app, "--device", "usb:0483:5720")

		wantFailure(t, got, want)
	})
}

// A printer its device cannot tell apart from another that matches gets no
// command: one with no serial, or one whose serial another has too.
func TestOnWindowsInstallGivesNoCommandForAPrinterItCannotName(t *testing.T) {
	const leaveOne = "  A printer with no serial number cannot be chosen among its model: leave one plugged in and run the same command again.\n"
	for _, tc := range []struct {
		name, vid, pid string
		serials        [3]string
		want           string
	}{
		{"a known model sharing a serial", "1fc9", "2016", [3]string{"SAME", "SAME", "THIRD"},
			"error: 3 XP-365B printers are connected\n  printpipe config --device usb:1FC9:2016:THIRD\n" + leaveOne},
		{"an unknown model with no serial", "0483", "5720", [3]string{"", "", "THIRD"},
			"error: 3 printers match usb:0483:5720\n  printpipe config --device usb:0483:5720:THIRD\n" + leaveOne},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := onWindows()
			for i, serial := range tc.serials {
				plugUSB(sys, fmt.Sprintf("1-%d", 7+i), tc.vid, tc.pid, serial)
			}
			got := run(t, sys, "install", "--origin", app, "--device", "usb:"+tc.vid+":"+tc.pid)
			wantFailure(t, got, tc.want)
		})
	}
}

// plugUSB plugs a USB printer in at a sysfs device; an empty serial is none.
func plugUSB(sys *fakeSystem, device, vid, pid, serial string) {
	const d = "/sys/bus/usb/devices/"
	sys.files[d+device+"/idVendor"] = []byte(vid + "\n")
	sys.files[d+device+"/idProduct"] = []byte(pid + "\n")
	if serial != "" {
		sys.files[d+device+"/serial"] = []byte(serial + "\n")
	}
	sys.files[d+device+":1.0/bInterfaceClass"] = []byte("07\n")
}

// secondXP plugs in another XP-365B.
func secondXP(sys *fakeSystem, serial string) { plugUSB(sys, "1-8", "1fc9", "2016", serial) }

// macOS.

func TestOnMacOSInstallLeavesAPlistForTheInstalledBinaryAndTakesOffQuarantine(t *testing.T) {
	sys, l := onMac()
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	sys.pathEnv = macExe[:strings.LastIndex(macExe, "/")]
	sys.executable = "/Users/me/Downloads/printpipe"
	sys.files[sys.executable] = []byte("this build")

	got := run(t, sys, "install", "--origin", app, "--no-test-print")

	if got.code != 0 {
		t.Fatalf("got %+v", got)
	}
	if l.program != macExe {
		t.Errorf("launchd runs %q, want %q", l.program, macExe)
	}
	if _, wrote := sys.files[loginPlistPath]; wrote {
		t.Error("install turned starting at login on")
	}
	wantRan(t, sys, "xattr -d com.apple.quarantine "+macExe)
	if strings.Contains(got.stdout, "PATH") {
		t.Errorf("stdout %q, want no PATH line", got.stdout)
	}
}

func TestOnMacOSInstallRefreshesTheCopyInLaunchAgentsWhenItIsThere(t *testing.T) {
	sys, _ := onMac()
	cupsComputer(sys, map[string]string{"XP-365B": usableQueue}, "")
	sys.files[loginPlistPath] = []byte("an older plist")

	got := run(t, sys, "install", "--origin", app, "--no-test-print")

	if got.code != 0 || !strings.HasSuffix(got.stdout, "Starts at login: on\n") {
		t.Errorf("got %+v", got)
	}
	if string(sys.files[loginPlistPath]) != string(plistOf(macExe)) {
		t.Errorf("LaunchAgents has\n%s", sys.files[loginPlistPath])
	}
}

// Choosing a printer among several on macOS and Linux, where a usb: device is not
// printed to: install makes a queue for it.

const secondURI = "usb://Xprinter/XP-365B?serial=SECOND&interface=1"

func TestInstallCreatesTheQueueForTheChosenDevice(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cupsComputer(sys, map[string]string{}, "direct "+xpURI+"\ndirect "+secondURI+"\n")
	secondXP(sys, "SECOND")

	got := runInstall(t, sys, "--device", "usb:1FC9:2016:second")

	if got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}
	ran := sudoCommands(sys)
	if len(ran) != 2 || !strings.Contains(ran[1], "-v "+secondURI+" ") {
		t.Errorf("ran as root %q, want a queue for the chosen printer", ran)
	}
	if cfg := savedConfig(t, sys); cfg.Queue != "XP-365B" || cfg.Device != "" {
		t.Errorf("config has queue %q and device %q, want the queue only", cfg.Queue, cfg.Device)
	}
}

func TestInstallWithTheChosenDevicesQueueThereAsksForNoPassword(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cupsComputer(sys, map[string]string{
		"first":  usableQueue,
		"second": "device-uri=" + secondURI + " printer-is-accepting-jobs=true",
	}, "")
	secondXP(sys, "SECOND")

	got := runInstall(t, sys, "--device", "usb:1FC9:2016:SECOND")

	if got.code != 0 || !strings.Contains(got.stdout, "Printer: queue second\n") {
		t.Errorf("got %+v, want exit 0 and the queue that prints to it", got)
	}
	if len(sudoCommands(sys)) != 0 {
		t.Errorf("ran as root %q", sudoCommands(sys))
	}
}

func TestInstallCommandsForSeveralPrintersCanBeRunAsPrinted(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cupsComputer(sys, map[string]string{}, "direct "+xpURI+"\ndirect "+secondURI+"\n")
	secondXP(sys, "SECOND")

	got := runInstall(t, sys)

	wantFailure(t, got, "error: 2 XP-365B printers are connected\n"+
		"  printpipe install --device usb:1FC9:2016:0020426981AA\n"+
		"  printpipe install --device usb:1FC9:2016:SECOND\n")
	if cfg := savedConfig(t, sys); !slices.Equal(cfg.Origins, []string{app}) {
		t.Errorf("config has origins %q: the commands name none", cfg.Origins)
	}

	line := strings.Split(got.stderr, "\n")[2]
	again := run(t, sys, append(strings.Fields(line)[1:], "--no-test-print")...)
	if again.code != 0 {
		t.Errorf("%s: %+v", line, again)
	}
	if cfg := savedConfig(t, sys); cfg.Queue == "" || cfg.Device != "" {
		t.Errorf("config has queue %q and device %q", cfg.Queue, cfg.Device)
	}
}

func TestInstallWithAChosenDeviceThatCannotBeUsedSaysWhy(t *testing.T) {
	for _, tc := range []struct{ name, device, want string }{
		{"not plugged in", "usb:1FC9:2016:OTHER", "error: the printer usb:1FC9:2016:OTHER is not connected\n  Plug it in, switch it on and run the same command again.\n"},
		{"a model printpipe does not know", "usb:04B8:0202", "error: usb:04B8:0202 is not a printer model printpipe knows\n  An existing queue: printpipe install --queue <name>\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			withSystemd(sys)
			cupsComputer(sys, map[string]string{}, "direct "+xpURI+"\n")

			got := runInstall(t, sys, "--device", tc.device)

			wantFailure(t, got, tc.want)
			if len(sudoCommands(sys)) != 0 {
				t.Errorf("ran as root %q", sudoCommands(sys))
			}
		})
	}
}

func TestInstallWithADeviceWithoutASerialAmongSeveralGivesACommandPerDevice(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cupsComputer(sys, map[string]string{}, "")
	secondXP(sys, "SECOND")

	got := runInstall(t, sys, "--device", "usb:1FC9:2016")

	if got.code != 1 || !strings.Contains(got.stderr, "printpipe install --device usb:1FC9:2016:SECOND\n") {
		t.Errorf("got %+v, want exit 1 and a command per device", got)
	}
}

// Only one model is known yet, so the wording for two is checked here.
func TestSeveralConnectedDoesNotCallDifferentModelsOneModel(t *testing.T) {
	a := models.Model{Name: "A-1", VID: 1, PID: 1}
	b := models.Model{Name: "B-2", VID: 2, PID: 2}

	err := severalConnected("linux", []knownPrinter{
		{a, printer.USBID{VID: 1, PID: 1, Serial: "S1"}},
		{b, printer.USBID{VID: 2, PID: 2}},
	})

	var fail *failure
	if !errors.As(err, &fail) {
		t.Fatalf("got %v", err)
	}
	want := []string{"printpipe install --device usb:0001:0001:S1", "printpipe install --device usb:0002:0002"}
	if fail.msg != "2 known label printers are connected" || !slices.Equal(fail.hints, want) {
		t.Errorf("got %q and %q, want %q", fail.msg, fail.hints, want)
	}
}

// A failed choice must not cost the printer that worked.
func TestInstallWithAChosenDeviceThatFailsKeepsTheSavedDevice(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys)
	cups(sys, map[string]string{}, "")
	sys.files[configPath] = []byte(`{"origins": ["` + app + `"], "device": "/dev/usb/lp0"}`)

	if got := run(t, sys, "install", "--device", "usb:1FC9:2016:OTHER"); got.code != 1 {
		t.Fatalf("got %+v, want exit 1", got)
	}
	if cfg := savedConfig(t, sys); cfg.Device != "/dev/usb/lp0" {
		t.Errorf("config has device %q, want the saved one", cfg.Device)
	}
}

func TestInstallThatCannotRefreshStartingAtLoginLeavesARunningAgentsPortInTheFile(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	files, _ := filesOf(sys)
	sys.files[files.Config] = []byte(`{"origins": ["` + app + `"]}`)
	runWindowsAgent(sys)
	sys.runValues["printpipe"] = "something"
	sys.runValueErr = errors.New("Access is denied.")

	got := run(t, sys, "install", "--port", "9100")

	wantFailure(t, got, "error: Access is denied.\n")
	if cfg := savedConfig(t, sys); cfg.Port != 8631 {
		t.Errorf("config has port %d, want the agent's", cfg.Port)
	}
}

func TestOnWindowsInstallThatCannotTakeTheMarkOffSaysHow(t *testing.T) {
	sys := onWindows()
	xpOnWindows(sys)
	sys.removeErr = map[string]error{windowsExe + ":Zone.Identifier": errors.New("Access is denied.")}

	got := run(t, sys, "install", "--origin", app)

	wantFailure(t, got, "error: could not take the download mark off "+windowsExe+": Access is denied.\n"+
		"  Windows will warn about it at logon. In PowerShell: Unblock-File '"+windowsExe+"'\n"+
		"  Then run the same command again.\n")
}

// What runs at login is the installed binary, so with none there is nothing it
// could run: better said now than found out at the next login.
func TestACopyWithNothingInstalledSaysSoInsteadOfPointingAtNothing(t *testing.T) {
	const want = "error: printpipe is not installed, and only what is installed starts by itself\n" +
		"  Install it: printpipe install --origin <url>\n"
	t.Run("startup on with a manager", func(t *testing.T) {
		sys := newFakeSystem()
		withSystemd(sys)
		downloadedCopy(sys)
		wantFailure(t, run(t, sys, "startup", "on"), want)
		wantNotRan(t, sys, "systemctl --user enable printpipe.service")
	})
	t.Run("start with a manager", func(t *testing.T) {
		sys := newFakeSystem()
		withSystemd(sys)
		downloadedCopy(sys)
		wantFailure(t, run(t, sys, "start"), want)
		wantNotRan(t, sys, "systemctl --user start printpipe.service")
	})
	t.Run("startup on without a manager", func(t *testing.T) {
		sys := noManager()
		downloadedCopy(sys)
		wantFailure(t, run(t, sys, "startup", "on"), want)
	})
	t.Run("startup on on Windows", func(t *testing.T) {
		sys := onWindows()
		downloadedOnWindows(sys)
		wantFailure(t, run(t, sys, "startup", "on"), want)
		if len(sys.runValues) != 0 {
			t.Errorf("the Run key has %v", sys.runValues)
		}
	})
	t.Run("start on a Mac", func(t *testing.T) {
		sys, _ := onMac()
		sys.executable = "/Users/me/Downloads/printpipe"
		wantFailure(t, run(t, sys, "start"), want)
	})
}

// A copy with no manager to hand it to runs by itself, as it always did.
func TestACopyWithNothingInstalledCanStillBeStartedByHand(t *testing.T) {
	sys := noManager()
	downloadedCopy(sys)

	wantOutput(t, run(t, sys, "start"), "Agent started, version dev\n")
	if !slices.Equal(sys.spawned, []string{downloadedExe + " serve"}) {
		t.Errorf("spawned %q", sys.spawned)
	}
}
