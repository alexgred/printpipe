//go:build !windows

package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexgred/printpipe/internal/server"
)

func TestStatusShowsARunningAgentAndWhereEverythingIs(t *testing.T) {
	sys, d := withXPQueue()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "XP-365B"}`)
	d.runUnit()
	d.enabled = true

	wantOutput(t, run(t, sys, "status"), `Agent: running under systemd, pid 4242
Version: installed dev, running dev
Port: 8631
Starts at login: on
Printer: queue XP-365B, exists
Binary: /home/me/.local/bin/printpipe
Config: /home/me/.config/printpipe/config.json
Log: /home/me/.local/state/printpipe/agent.log
`)
}

// wantLine checks that a command's output has this whole line.
func wantLine(t *testing.T, got result, line string) {
	t.Helper()
	if !strings.Contains("\n"+got.stdout, "\n"+line+"\n") {
		t.Errorf("stdout %q, want the line %q", got.stdout, line)
	}
}

// runAgentOfVersion makes an agent of this version answer on the port.
func runAgentOfVersion(sys *fakeSystem, version string) {
	sys.runAgent(&fakePrinter{})
	sys.onPort = server.Handler(server.Options{Version: version, PID: agentPID}, &fakePrinter{})
	sys.exes[agentPID] = installedExe
}

func TestStatusSuggestsRestartWhenTheRunningVersionIsNotTheInstalledOne(t *testing.T) {
	sys, d := withXPQueue()
	d.runUnit()
	runAgentOfVersion(sys, "0.1.0")

	got := run(t, sys, "status")

	if got.code != 0 {
		t.Errorf("got %+v, want exit 0", got)
	}
	if !strings.Contains(got.stdout, "Version: installed dev, running 0.1.0\n  The agent runs another version than is installed: printpipe restart\nPort:") {
		t.Errorf("stdout %q, want both versions and the suggestion", got.stdout)
	}
}

func TestStatusSaysWhatRunsTheAgent(t *testing.T) {
	t.Run("nothing, without a user manager", func(t *testing.T) {
		sys := noManager()
		runStrayAgent(sys)
		wantLine(t, run(t, sys, "status"), "Agent: running with no service manager, pid 4242")
	})
	t.Run("nothing, in a terminal beside a user manager", func(t *testing.T) {
		sys := newFakeSystem()
		withSystemd(sys)
		runStrayAgent(sys)
		wantLine(t, run(t, sys, "status"), "Agent: running with no service manager, pid 4242")
	})
	t.Run("launchd", func(t *testing.T) {
		sys, l := onMac()
		l.runService()
		wantLine(t, run(t, sys, "status"), "Agent: running under launchd, pid 4242")
	})
	t.Run("nothing, in a terminal on a Mac", func(t *testing.T) {
		sys, _ := onMac()
		runStrayMacAgent(sys)
		wantLine(t, run(t, sys, "status"), "Agent: running with no service manager, pid 4242")
	})
	t.Run("nothing, on Windows", func(t *testing.T) {
		sys := onWindows()
		runWindowsAgent(sys)
		wantLine(t, run(t, sys, "status"), "Agent: running with no service manager, pid 4242")
	})
}

func TestStatusSaysWhetherTheOSHasTheConfiguredPrinter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		setup  func(sys *fakeSystem)
		want   string
	}{
		{"no printer", `{}`, nil, "Printer: (none)"},
		{"a queue CUPS has, in other letters", `{"queue": "xp-365b"}`,
			func(sys *fakeSystem) { cups(sys, map[string]string{"XP-365B": usableQueue}, "") },
			"Printer: queue xp-365b, exists"},
		{"a queue CUPS does not have", `{"queue": "gone"}`,
			func(sys *fakeSystem) { cups(sys, map[string]string{"XP-365B": usableQueue}, "") },
			"Printer: queue gone, not found"},
		{"CUPS not installed", `{"queue": "XP-365B"}`, nil,
			"Printer: queue XP-365B, could not check: CUPS is not installed"},
		{"the scheduler down", `{"queue": "XP-365B"}`,
			func(sys *fakeSystem) {
				sys.programs["lpstat"] = func([]string) ([]byte, error) { return []byte("scheduler is not running\n"), nil }
			},
			"Printer: queue XP-365B, could not check: the CUPS scheduler is not running"},
		{"a device file that is there", `{"device": "/dev/usb/lp0"}`,
			func(sys *fakeSystem) { sys.files["/dev/usb/lp0"] = nil },
			"Printer: device /dev/usb/lp0, exists"},
		{"a device file that is not there, beside another", `{"device": "/dev/usb/lp1"}`,
			func(sys *fakeSystem) { sys.files["/dev/usb/lp0"] = nil },
			"Printer: device /dev/usb/lp1, not found"},
		{"a device file in a folder that is not there", `{"device": "/dev/usb/lp0"}`, nil,
			"Printer: device /dev/usb/lp0, not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := noManager()
			sys.files[configPath] = []byte(tc.config)
			if tc.setup != nil {
				tc.setup(sys)
			}
			runStrayAgent(sys)

			got := run(t, sys, "status")

			wantLine(t, got, tc.want)
			if got.code != 0 {
				t.Errorf("got %+v, want exit 0: only a stopped agent fails", got)
			}
		})
	}
}

func TestStatusOnWindowsSaysWhetherTheUSBDeviceIsThere(t *testing.T) {
	for _, tc := range []struct {
		name, device, want string
		plugged            bool
	}{
		{"connected", "usb:1FC9:2016", "Printer: XP-365B (usb:1FC9:2016), exists", true},
		{"not connected", "usb:1FC9:2016", "Printer: XP-365B (usb:1FC9:2016), not found", false},
		{"not a device", "usb:nope", `Printer: device usb:nope, could not check: `, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := onWindows()
			if tc.plugged {
				xpOnWindows(sys)
			}
			sys.files[`C:\Users\me\AppData\Roaming\printpipe\config.json`] = []byte(`{"device": "` + tc.device + `"}`)
			runWindowsAgent(sys)

			got := run(t, sys, "status")

			if !strings.Contains(got.stdout, tc.want) {
				t.Errorf("stdout %q, want %q", got.stdout, tc.want)
			}
		})
	}
}

func TestStatusAsksTheComputerAndNotThePrinterOrTheNetwork(t *testing.T) {
	sys, d := withXPQueue()
	sys.files[configPath] = []byte(`{"queue": "XP-365B"}`)
	d.runUnit()

	if got := run(t, sys, "status"); got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}

	if !slices.Equal(sys.requested, []string{"http://127.0.0.1:8631/health"}) {
		t.Errorf("requested %q, want only the agent's /health", sys.requested)
	}
	for _, command := range sys.ranCommands() {
		if strings.HasPrefix(command, "lp ") || strings.HasPrefix(command, "lpoptions ") {
			t.Errorf("asked the printer's queue: %q", command)
		}
	}
	if len(sys.written) != 0 {
		t.Errorf("wrote to devices: %q", sys.written)
	}
}

func TestStatusFromACopyAsksTheInstalledBinaryForItsVersion(t *testing.T) {
	sys := noManager()
	downloadedCopy(sys)
	sys.files[installedExe] = []byte("the installed build")
	sys.programs[installedExe] = func(args []string) ([]byte, error) {
		if !slices.Equal(args, []string{"version"}) {
			t.Errorf("the installed binary got %q", args)
		}
		return []byte("printpipe 0.2.0\n"), nil
	}
	runAgentOfVersion(sys, "0.2.0")

	got := run(t, sys, "status")

	wantLine(t, got, "Version: installed 0.2.0, running 0.2.0")
}

func TestStatusFromACopyWithNothingInstalledSuggestsNoRestart(t *testing.T) {
	sys := noManager()
	downloadedCopy(sys)
	runStrayAgent(sys)

	got := run(t, sys, "status")

	wantLine(t, got, "Version: installed (none), running dev")
	if strings.Contains(got.stdout, "restart") {
		t.Errorf("stdout %q suggests a restart, which would run the same version", got.stdout)
	}
}

func TestStatusSaysWhenTheInstalledBinaryDoesNotTellItsVersion(t *testing.T) {
	sys := noManager()
	downloadedCopy(sys)
	sys.files[installedExe] = []byte("damaged")
	sys.programs[installedExe] = func([]string) ([]byte, error) { return nil, errors.New("exec format error") }
	runStrayAgent(sys)

	got := run(t, sys, "status")

	wantLine(t, got, "Version: installed (unknown), running dev")
	if strings.Contains(got.stdout, "restart") {
		t.Errorf("stdout %q suggests a restart without knowing the versions differ", got.stdout)
	}
}

func TestStatusWithAnotherProgramOnThePortShowsTheRestAndExitsWithOne(t *testing.T) {
	sys, _ := withXPQueue()
	sys.files[configPath] = []byte(`{"queue": "XP-365B"}`)
	sys.runForeignProgram()

	got := run(t, sys, "status")

	wantFailure(t, got, "error: port 8631 is used by another program\n"+
		"  Close that program and run the same command again.\n"+
		"  Windows: netstat -ano | findstr :8631   macOS/Linux: lsof -i :8631\n")
	wantLine(t, got, "Agent: not running")
	wantLine(t, got, "Printer: queue XP-365B, exists")
}

// Another user's agent holds the port, so this user's agent is not running.
func TestStatusWithAProcessItCannotSeeOnThePortNamesItsPidAndExitsWithOne(t *testing.T) {
	sys, _ := withXPQueue()
	sys.files[configPath] = []byte(`{"queue": "XP-365B"}`)
	sys.runAgent(&fakePrinter{})

	got := run(t, sys, "status")

	wantFailure(t, got, "error: could not check process 4242 on port 8631: permission denied\n"+
		"  It may be another user's agent.\n")
	wantLine(t, got, "Agent: not running")
	wantLine(t, got, "Printer: queue XP-365B, exists")
}

func TestStatusShowsThatItCannotReadTheConfig(t *testing.T) {
	sys, _ := withXPQueue()
	sys.files[configPath] = []byte(`{"port": `)

	wantFailure(t, run(t, sys, "status"), "error: "+configPath+": unexpected end of JSON input\n")
}

func TestStatusWithTheAgentStoppedShowsTheRestAndExitsWithOne(t *testing.T) {
	sys, _ := withXPQueue()
	sys.files[configPath] = []byte(`{"queue": "XP-365B"}`)

	got := run(t, sys, "status")

	wantFailure(t, got, "error: the agent is not running\n  Start it: printpipe start\n")
	want := `Agent: not running
Version: installed dev
Port: 8631
Starts at login: off
Printer: queue XP-365B, exists
Binary: /home/me/.local/bin/printpipe
Config: /home/me/.config/printpipe/config.json
Log: /home/me/.local/state/printpipe/agent.log
`
	if got.stdout != want {
		t.Errorf("stdout %q, want %q", got.stdout, want)
	}
}
