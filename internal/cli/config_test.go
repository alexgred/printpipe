package cli

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
)

const notRunning = "The agent is not running: the change applies when it starts.\n"

func TestConfigWithoutFlagsShowsTheDefaultsBeforeAnyFileExists(t *testing.T) {
	sys := newFakeSystem()

	wantOutput(t, run(t, sys, "config"), "Port: 8631\nOrigins: (none)\nQueue: (none)\nDevice: (none)\n")
	if len(sys.files) != 0 {
		t.Errorf("showing wrote files: %q", sys.files)
	}
}

func TestConfigWithoutFlagsShowsTheConfiguration(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"port": 9100, "origins": ["https://a.example", "https://b.example"], "queue": "label"}`)

	wantOutput(t, run(t, sys, "config"), "Port: 9100\nOrigins: https://a.example, https://b.example\nQueue: label\nDevice: (none)\n")
}

func TestConfigShowsThatItCannotReadTheFile(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"port": `)

	wantFailure(t, run(t, sys, "config"), "error: "+configPath+": unexpected end of JSON input\n")
}

// wantShows checks that printpipe config now shows these lines, which is what
// config.json holds as the next start of the agent reads it.
func wantShows(t *testing.T, sys *fakeSystem, lines ...string) {
	t.Helper()
	got := run(t, sys, "config")
	for _, line := range lines {
		if !strings.Contains("\n"+got.stdout, "\n"+line+"\n") {
			t.Errorf("config shows %q, want the line %q", got.stdout, line)
		}
	}
}

func TestConfigWritesTheChangeAndSaysWhatChangedWhenStopped(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"port": 9100, "origins": ["https://a.example"], "queue": "old"}`)

	wantOutput(t, run(t, sys, "config", "--queue", "label"), "Queue: old -> label\n"+notRunning)

	wantShows(t, sys, "Port: 9100", "Origins: https://a.example", "Queue: label", "Device: (none)")
	if len(sys.spawned) != 0 {
		t.Errorf("a stopped agent was started: %q", sys.spawned)
	}
}

func TestConfigWritesReadableJSONAndNoOtherFile(t *testing.T) {
	sys := noManager()

	run(t, sys, "config", "--queue", "label")

	want := "{\n  \"port\": 8631,\n  \"origins\": [],\n  \"queue\": \"label\"\n}\n"
	if got := string(sys.files[configPath]); got != want {
		t.Errorf("config.json is %q, want %q", got, want)
	}
	if len(sys.files) != 1 {
		t.Errorf("files %q, want only config.json", slices.Collect(maps.Keys(sys.files)))
	}
}

// The agent refuses a config with both, and a queue is what install's hints
// offer in place of a device, and the other way round.
func TestConfigQueueAndDeviceReplaceEachOther(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"queue": "label"}`)

	wantOutput(t, run(t, sys, "config", "--device", "/dev/usb/lp0"), "Queue: label -> (none)\nDevice: (none) -> /dev/usb/lp0\n"+notRunning)
	wantShows(t, sys, "Queue: (none)", "Device: /dev/usb/lp0")

	wantOutput(t, run(t, sys, "config", "--queue", "label"), "Queue: (none) -> label\nDevice: /dev/usb/lp0 -> (none)\n"+notRunning)
	wantShows(t, sys, "Queue: label", "Device: (none)")
}

func TestConfigAddsOriginsAndIgnoresDuplicates(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"origins": ["https://a.example"]}`)

	wantOutput(t, run(t, sys, "config", "--origin", "https://a.example", "--origin", "https://b.example", "--origin", "https://b.example"), "Origins: added https://b.example\n"+notRunning)

	wantShows(t, sys, "Origins: https://a.example, https://b.example")
}

func TestConfigRemovesAnOrigin(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"origins": ["https://a.example", "https://b.example"]}`)

	wantOutput(t, run(t, sys, "config", "--remove-origin", "https://a.example"), "Origins: removed https://a.example\n"+notRunning)

	wantShows(t, sys, "Origins: https://b.example")
}

// A mistyped origin must not look like it was removed.
func TestConfigSaysAnOriginToRemoveIsNotThere(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"origins": ["https://a.example"]}`)

	wantOutput(t, run(t, sys, "config", "--remove-origin", "https://typo.example"), "Not in origins: https://typo.example\nNothing changed.\n")
}

// A hand-written origin that could never match has to be removable.
func TestConfigRemovesAnOriginThatIsNotValid(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"origins": ["https://a.example/", "https://b.example"]}`)

	wantOutput(t, run(t, sys, "config", "--remove-origin", "https://a.example/"), "Origins: removed https://a.example/\n"+notRunning)
}

// The agent refuses to run with no origin at all.
func TestConfigKeepsTheLastOrigin(t *testing.T) {
	sys := noManager()
	before := `{"origins": ["https://a.example"]}`
	sys.files[configPath] = []byte(before)

	wantFailure(t, run(t, sys, "config", "--remove-origin", "https://a.example"), "error: cannot remove the last origin: no page could print\n"+
		"  Replace it: printpipe config --origin <url> --remove-origin https://a.example\n")
	if got := string(sys.files[configPath]); got != before {
		t.Errorf("config.json is now %q", got)
	}
}

// The hint is a command to run, so it names an origin that is there.
func TestConfigKeepingTheLastOriginNamesItNotATypo(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"origins": ["https://a.example"]}`)

	wantFailure(t, run(t, sys, "config", "--remove-origin", "https://typo.example", "--remove-origin", "https://a.example"), "error: cannot remove the last origin: no page could print\n"+
		"  Replace it: printpipe config --origin <url> --remove-origin https://a.example\n")
}

func TestConfigReplacesTheLastOrigin(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"origins": ["https://a.example"]}`)

	wantOutput(t, run(t, sys, "config", "--origin", "https://b.example", "--remove-origin", "https://a.example"), "Origins: added https://b.example\nOrigins: removed https://a.example\n"+notRunning)
}

// Nothing to apply is no reason to take a running agent down.
func TestConfigWithNoChangeLeavesTheFileAndTheAgentAlone(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys).runUnit()
	before := `{"port": 8631, "origins": ["https://a.example"], "queue": "label"}`
	sys.files[configPath] = []byte(before)

	wantOutput(t, run(t, sys, "config", "--origin", "https://a.example", "--queue", "label", "--port", "8631"), "Nothing changed.\n")
	wantNotRan(t, sys, "systemctl --user restart printpipe.service")
	if got := string(sys.files[configPath]); got != before {
		t.Errorf("config.json is now %q", got)
	}
}

func TestConfigRefusesWhatCouldNotWorkAndChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		goos  string
		args  []string
		error string
	}{
		{"port not a number", "linux", []string{"--port", "x"}, `--port "x" is not a port: want 1 to 65535`},
		{"port zero", "linux", []string{"--port", "0"}, `--port "0" is not a port: want 1 to 65535`},
		{"port too high", "linux", []string{"--port", "65536"}, `--port "65536" is not a port: want 1 to 65535`},
		{"no queue name", "linux", []string{"--queue="}, "--queue needs a name"},
		{"queue with a space on CUPS", "linux", []string{"--queue", "My Printer"}, `--queue "My Printer": a CUPS queue's name has no space, tab, / or #`},
		{"queue like a flag of lp", "darwin", []string{"--queue", "-x"}, `--queue "-x": a queue's name does not start with -`},
		{"no device", "linux", []string{"--device="}, "--device needs a value"},
		{"usb device on Linux", "linux", []string{"--device", "usb:1FC9:2016"}, `--device "usb:1FC9:2016": on Linux use a device file such as /dev/usb/lp0, or --queue`},
		{"relative device on Linux", "linux", []string{"--device", "lp0"}, `--device "lp0": on Linux use a device file such as /dev/usb/lp0, or --queue`},
		{"device on macOS", "darwin", []string{"--device", "/dev/usb/lp0"}, `--device "/dev/usb/lp0": on macOS use --queue`},
		{"file on Windows", "windows", []string{"--device", "/dev/usb/lp0"}, `device "/dev/usb/lp0": not a USB device; want usb:VID:PID or usb:VID:PID:serial, such as usb:1FC9:2016`},
		{"bad usb on Windows", "windows", []string{"--device", "usb:1FC9"}, `device "usb:1FC9": not two or three parts; want usb:VID:PID or usb:VID:PID:serial, such as usb:1FC9:2016`},
		{"queue and device", "linux", []string{"--queue", "a", "--device", "/dev/usb/lp0"}, "use either --queue or --device, not both"},
		{"add and remove the same", "linux", []string{"--origin", "https://a.example", "--remove-origin", "https://a.example"}, "--origin and --remove-origin both name https://a.example"},
		{"origin without a scheme", "linux", []string{"--origin", "app.example"}, badOrigin("app.example")},
		{"origin of another scheme", "linux", []string{"--origin", "ftp://app.example"}, badOrigin("ftp://app.example")},
		{"origin without a host", "linux", []string{"--origin", "https://"}, badOrigin("https://")},
		{"origin with a slash", "linux", []string{"--origin", "https://app.example/"}, badOrigin("https://app.example/")},
		{"origin with a path", "linux", []string{"--origin", "https://app.example/print"}, badOrigin("https://app.example/print")},
		{"origin with a query", "linux", []string{"--origin", "https://app.example?a=1"}, badOrigin("https://app.example?a=1")},
		{"origin with a user", "linux", []string{"--origin", "https://me@app.example"}, badOrigin("https://me@app.example")},
		{"origin in capitals", "linux", []string{"--origin", "https://App.example"}, badOrigin("https://App.example")},
		{"empty origin", "linux", []string{"--origin="}, badOrigin("")},
		{"https with its default port", "linux", []string{"--origin", "https://app.example:443"}, badOrigin("https://app.example:443")},
		{"http with its default port", "linux", []string{"--origin", "http://app.example:80"}, badOrigin("http://app.example:80")},
		{"origin with an empty port", "linux", []string{"--origin", "https://app.example:"}, badOrigin("https://app.example:")},
		{"origin with no such port", "linux", []string{"--origin", "http://localhost:99999"}, badOrigin("http://localhost:99999")},
		{"origin not in punycode", "linux", []string{"--origin", "https://пример.рф"}, badOrigin("https://пример.рф")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := noManager()
			sys.goos = tc.goos
			before := `{"origins": ["https://a.example"], "queue": "label"}`
			sys.files[configPath] = []byte(before)

			got := run(t, sys, append([]string{"config"}, tc.args...)...)

			want := "error: " + tc.error + "\n  Run printpipe help to see the commands.\n"
			if got.code != 2 || got.stdout != "" || got.stderr != want {
				t.Errorf("got %+v, want exit 2 and %q", got, want)
			}
			if got := string(sys.files[configPath]); got != before {
				t.Errorf("config.json is now %q", got)
			}
		})
	}
}

func badOrigin(origin string) string {
	return fmt.Sprintf("--origin %q is not an origin: want the scheme and host a browser sends, in small letters, such as https://app.example or http://localhost:3000", origin)
}

func TestConfigTakesTheDevicesOfEachOS(t *testing.T) {
	for _, tc := range []struct{ goos, device, written string }{
		{"linux", "/dev/usb/lp0", "/dev/usb/lp0"},
		{"windows", "usb:1FC9:2016", "usb:1FC9:2016"},
		{"windows", "usb:1fc9:2016:ab1", "usb:1FC9:2016:ab1"},
	} {
		t.Run(tc.goos+" "+tc.device, func(t *testing.T) {
			sys := noManager()
			sys.goos = tc.goos

			if got := run(t, sys, "config", "--device", tc.device); got.code != 0 {
				t.Fatalf("got %+v", got)
			}
			wantShows(t, sys, "Device: "+tc.written)
		})
	}
}

func TestConfigTakesAWindowsPrinterNameWithASpace(t *testing.T) {
	sys := noManager()
	sys.goos = "windows"

	if got := run(t, sys, "config", "--queue", "Label Printer"); got.code != 0 {
		t.Fatalf("got %+v", got)
	}
	wantShows(t, sys, "Queue: Label Printer")
}

func TestConfigTakesOriginsABrowserSends(t *testing.T) {
	for _, origin := range []string{"https://app.example", "http://localhost:3000", "http://127.0.0.1:5173", "https://app.example:80", "http://app.example:443", "https://xn--e1afmkfd.xn--p1ai", "http://[::1]:3000"} {
		sys := noManager()
		if got := run(t, sys, "config", "--origin", origin); got.code != 0 {
			t.Errorf("%s: got %+v", origin, got)
		}
	}
}

func TestConfigWarnsThatAClientMustUseTheNewPort(t *testing.T) {
	sys := noManager()

	wantOutput(t, run(t, sys, "config", "--port", "9100"), "Port: 8631 -> 9100\n"+portWarning(9100)+notRunning)
	wantShows(t, sys, "Port: 9100")
}

func portBusyError(port int) string {
	return fmt.Sprintf("error: port %d is used by another program\n"+
		"  Close that program and run the same command again.\n"+
		"  Windows: netstat -ano | findstr :%d   macOS/Linux: lsof -i :%d\n", port, port, port)
}

func portWarning(port int) string {
	return fmt.Sprintf("Warning: a client's agent URL must use the same port: http://127.0.0.1:%d\n", port)
}

func TestConfigRestartsARunningAgentWithTheChange(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys).runUnit()

	wantOutput(t, run(t, sys, "config", "--queue", "label"), "Queue: (none) -> label\nAgent restarted, version dev\n")
	wantRan(t, sys, "systemctl --user restart printpipe.service")
	wantShows(t, sys, "Queue: label")
}

// The agent is on the old port until it restarts, and on the new one after.
func TestConfigPortMovesARunningAgentUnderAManager(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys).runUnit()

	wantOutput(t, run(t, sys, "config", "--port", "9100"), "Port: 8631 -> 9100\n"+portWarning(9100)+"Agent restarted, version dev\n")
	wantRan(t, sys, "systemctl --user restart printpipe.service")
	if sys.agentPort != "9100" {
		t.Errorf("the agent runs on port %s", sys.agentPort)
	}
}

func TestConfigPortMovesARunningAgentWithoutAManager(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)

	wantOutput(t, run(t, sys, "config", "--port", "9100"), "Port: 8631 -> 9100\n"+portWarning(9100)+"Agent restarted, version dev\n")
	if !slices.Equal(sys.terminated, []int{agentPID}) || !slices.Equal(sys.spawned, []string{installedExe + " serve"}) {
		t.Errorf("terminated %v, spawned %q", sys.terminated, sys.spawned)
	}
	if sys.agentPort != "9100" {
		t.Errorf("the agent runs on port %s", sys.agentPort)
	}
}

func TestConfigPortMovesARunningAgentOnWindows(t *testing.T) {
	sys := onWindows()
	runWindowsAgent(sys)

	wantOutput(t, run(t, sys, "config", "--port", "9100"), "Port: 8631 -> 9100\n"+portWarning(9100)+"Agent restarted, version dev\n")
	if len(sys.killed) != 0 {
		t.Errorf("killed %v: the stop should have seen the old port close", sys.killed)
	}
	if sys.agentPort != "9100" {
		t.Errorf("the agent runs on port %s", sys.agentPort)
	}
}

// A working agent is not taken down for a port it could not get.
func TestConfigKeepsARunningAgentWhenTheNewPortIsTaken(t *testing.T) {
	sys := newFakeSystem()
	withSystemd(sys).runUnit()
	sys.otherPorts = map[string]http.Handler{"9100": foreignProgram}
	before := `{"origins": ["https://a.example"], "queue": "label"}`
	sys.files[configPath] = []byte(before)

	wantFailure(t, run(t, sys, "config", "--port", "9100"), portBusyError(9100))
	wantNotRan(t, sys, "systemctl --user restart printpipe.service")
	if got := string(sys.files[configPath]); got != before {
		t.Errorf("config.json is now %q", got)
	}
}

// Moving off a port another program holds is the way out, and the program is
// not printpipe's to touch.
func TestConfigMovesOffAPortAnotherProgramHolds(t *testing.T) {
	sys := noManager()
	sys.runForeignProgram()

	wantOutput(t, run(t, sys, "config", "--port", "9100"), "Port: 8631 -> 9100\n"+portWarning(9100)+notRunning)
	wantShows(t, sys, "Port: 9100")
	if len(sys.terminated)+len(sys.killed)+len(sys.spawned) != 0 {
		t.Errorf("terminated %v, killed %v, spawned %q", sys.terminated, sys.killed, sys.spawned)
	}
}

// The change is in the file by then, and output says so before the error.
func TestConfigReportsAnAgentThatDoesNotComeBack(t *testing.T) {
	sys := noManager()
	runStrayAgent(sys)
	sys.onSpawn = nil

	got := run(t, sys, "config", "--queue", "label")

	if got.code != 1 || got.stdout != "Queue: (none) -> label\n" {
		t.Errorf("got %+v, want exit 1 and what changed", got)
	}
	want := "error: the agent did not answer within 10 s\n  Log: /home/me/.local/state/printpipe/agent.log\n"
	if got.stderr != want {
		t.Errorf("stderr %q, want %q", got.stderr, want)
	}
	wantShows(t, sys, "Queue: label")
}

func TestConfigPortMovesARunningAgentOnAMac(t *testing.T) {
	sys, launchd := onMac()
	launchd.runService()

	wantOutput(t, run(t, sys, "config", "--port", "9100"), "Port: 8631 -> 9100\n"+portWarning(9100)+"Agent restarted, version dev\n")
	if sys.agentPort != "9100" {
		t.Errorf("the agent runs on port %s", sys.agentPort)
	}
}

// An agent left on the old port with the new one in the file could no longer
// be found by any command.
func TestConfigKeepsTheOldFileWhenTheAgentCannotBeRestarted(t *testing.T) {
	sys := noManager()
	sys.runAgent(&fakePrinter{}) // a process this user may not look at
	sys.files[configPath] = []byte(`{"origins": ["https://a.example"], "queue": "label"}`)

	got := run(t, sys, "config", "--port", "9100")

	if got.code != 1 || got.stdout != "config.json was left as it was.\n" {
		t.Errorf("got %+v, want exit 1 and that the file is as it was", got)
	}
	want := "error: could not check process 4242 on port 8631: permission denied\n  It may be another user's agent.\n"
	if got.stderr != want {
		t.Errorf("stderr %q, want %q", got.stderr, want)
	}
	wantShows(t, sys, "Port: 8631", "Origins: https://a.example", "Queue: label")
}

// start would refuse, so "when it starts" would be a promise it cannot keep.
func TestConfigSaysAnotherProgramHoldsThePortItStays(t *testing.T) {
	sys := noManager()
	sys.runForeignProgram()

	wantOutput(t, run(t, sys, "config", "--queue", "label"), "Queue: (none) -> label\n"+
		"The agent is not running, and port 8631 is used by another program: it cannot start until that is free.\n")
	wantShows(t, sys, "Queue: label")
}

// A file printpipe cannot read may hold what the user wants to keep.
func TestConfigDoesNotOverwriteAFileItCannotRead(t *testing.T) {
	sys := noManager()
	sys.files[configPath] = []byte(`{"port": `)

	wantFailure(t, run(t, sys, "config", "--queue", "label"), "error: "+configPath+": unexpected end of JSON input\n")
	if got := string(sys.files[configPath]); got != `{"port": ` {
		t.Errorf("config.json is now %q", got)
	}
}
