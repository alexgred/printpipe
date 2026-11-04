//go:build !windows

package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	linuxLog  = "/home/me/.local/state/printpipe/agent.log"
	wantsPath = "/home/me/.config/systemd/user/default.target.wants/printpipe.service"
)

// withCreatedQueue is a computer whose agent runs and prints to the queue
// XP-365B that printpipe created, among the queues Mine and that one.
func withCreatedQueue() (*fakeSystem, *fakeSystemd, map[string]string) {
	sys := newFakeSystem()
	d := withSystemd(sys)
	queues := map[string]string{"XP-365B": usableQueue, "Mine": usableQueue}
	cupsComputer(sys, queues, "")
	sys.files[installedExe] = []byte("installed")
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "XP-365B", "created_queue": "XP-365B"}`)
	d.runUnit()
	return sys, d, queues
}

func TestRemoveKeepsTheQueueItCreatedAndSaysHowToDeleteIt(t *testing.T) {
	sys, _, queues := withCreatedQueue()

	wantOutput(t, run(t, sys, "remove"), `Agent stopped
printpipe removed
Printer queue kept: XP-365B. To delete it: sudo lpadmin -x XP-365B
`)

	if _, ok := queues["XP-365B"]; !ok {
		t.Error("the queue was deleted")
	}
	if got := sudoCommands(sys); got != nil {
		t.Errorf("ran %q as root", got)
	}
}

func TestRemoveQueueDeletesTheQueueItCreatedAndNoOther(t *testing.T) {
	sys, _, queues := withCreatedQueue()

	wantOutput(t, run(t, sys, "remove", "--queue"), `Deleting the printer queue XP-365B; sudo may ask for your computer password.
Printer queue deleted: XP-365B
Agent stopped
printpipe removed
`)

	if _, ok := queues["XP-365B"]; ok {
		t.Error("the queue is still there")
	}
	if _, ok := queues["Mine"]; !ok {
		t.Error("another queue was deleted")
	}
	if got, want := sudoCommands(sys), []string{"lpadmin -x XP-365B"}; !slices.Equal(got, want) {
		t.Errorf("ran %q as root, want %q", got, want)
	}
	wantGone(t, sys, installedExe, configPath, unitPath)
}

func TestRemoveQueueNeverDeletesAQueueItDidNotCreate(t *testing.T) {
	sys, _, queues := withCreatedQueue()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "Mine"}`)

	wantOutput(t, run(t, sys, "remove", "--queue"), `printpipe created no printer queue here; none was deleted
Agent stopped
printpipe removed
`)

	if len(queues) != 2 {
		t.Errorf("queues left: %v", queues)
	}
	if got := sudoCommands(sys); got != nil {
		t.Errorf("ran %q as root", got)
	}
}

func TestRemoveQueueDeletesTheQueueItCreatedEvenWhenPrintingToAnother(t *testing.T) {
	sys, _, queues := withCreatedQueue()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "Mine", "created_queue": "XP-365B"}`)

	if got := run(t, sys, "remove", "--queue"); got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}

	if _, ok := queues["XP-365B"]; ok {
		t.Error("the created queue is still there")
	}
	if _, ok := queues["Mine"]; !ok {
		t.Error("the user's queue was deleted")
	}
}

func TestRemoveQueueWithTheQueueAlreadyGoneAsksForNoPassword(t *testing.T) {
	sys, _, queues := withCreatedQueue()
	delete(queues, "XP-365B")

	wantOutput(t, run(t, sys, "remove", "--queue"), `The printer queue XP-365B is already gone
Agent stopped
printpipe removed
`)

	if got := sudoCommands(sys); got != nil {
		t.Errorf("ran %q as root", got)
	}
}

func TestRemoveQueueWithoutCUPSRemovesTheRest(t *testing.T) {
	sys, _, _ := withCreatedQueue()
	delete(sys.programs, "lpstat")

	wantOutput(t, run(t, sys, "remove", "--queue"), `The printer queue XP-365B is already gone
Agent stopped
printpipe removed
`)
	wantGone(t, sys, installedExe, configPath)
}

// The queue's record is in the config, and sudo may be refused: nothing has gone
// yet when it fails, so that the same command works.
func TestRemoveQueueThatCannotDeleteTheQueueChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sudo   error
		stderr string
	}{
		{
			"sudo refused",
			errors.New("sudo: exit status 1: sudo: a password is required"),
			"error: could not delete the printer queue XP-365B: administrator rights were refused\n" +
				"  Run the same command again and enter your computer password when asked.\n",
		},
		{
			"lpadmin failed",
			errors.New("sudo: exit status 1: lpadmin: Forbidden"),
			"error: could not delete the printer queue XP-365B: sudo: exit status 1: lpadmin: Forbidden\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys, _, _ := withCreatedQueue()
			sys.programs["sudo"] = func([]string) ([]byte, error) { return nil, tc.sudo }

			got := run(t, sys, "remove", "--queue")

			wantFailure(t, got, tc.stderr)
			if sys.onPort == nil {
				t.Error("the agent was stopped")
			}
			for _, file := range []string{installedExe, configPath, unitPath} {
				if _, ok := sys.files[file]; !ok {
					t.Errorf("%s was removed", file)
				}
			}
		})
	}
}

func TestRemoveQueueWithTheSchedulerDownChangesNothing(t *testing.T) {
	sys, _, _ := withCreatedQueue()
	sys.programs["lpstat"] = func([]string) ([]byte, error) { return []byte("scheduler is not running\n"), nil }

	got := run(t, sys, "remove", "--queue")

	wantFailure(t, got, "error: could not read the printer queues: the CUPS scheduler is not running\n"+
		"  Check that CUPS is running and run the same command again.\n")
	if _, ok := sys.files[configPath]; !ok {
		t.Error("config.json was removed")
	}
}

// wantGone checks that none of these files is left.
func wantGone(t *testing.T, sys *fakeSystem, files ...string) {
	t.Helper()
	for _, file := range files {
		if _, ok := sys.files[file]; ok {
			t.Errorf("%s is still there", file)
		}
	}
}

func TestRemoveStopsTheAgentAndTakesEverythingOffTheComputer(t *testing.T) {
	sys, d := withXPQueue()
	sys.files[installedExe] = []byte("installed")
	sys.files[installedExe+"-sibling"] = []byte("someone else's")
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "XP-365B"}`)
	sys.files[linuxLog] = []byte("log")
	sys.files[linuxLog+".1"] = []byte("older log")
	sys.files["/home/me/.local/bin/.printpipe.old"] = []byte("old")
	sys.files[autostartPath] = []byte("[Desktop Entry]\n")
	d.runUnit()
	d.enabled = true

	wantOutput(t, run(t, sys, "remove"), "Agent stopped\nprintpipe removed\n")

	if sys.onPort != nil {
		t.Error("the agent still answers")
	}
	if d.enabled {
		t.Error("the unit is still enabled")
	}
	wantRan(t, sys, "systemctl --user reset-failed printpipe.service")
	wantRan(t, sys, "systemctl --user daemon-reload")
	wantGone(t, sys, installedExe, configPath, linuxLog, linuxLog+".1", "/home/me/.local/bin/.printpipe.old", unitPath, autostartPath)
	if _, ok := sys.files[installedExe+"-sibling"]; !ok {
		t.Error("a file that is not printpipe's was removed")
	}
	if got := sudoCommands(sys); got != nil {
		t.Errorf("ran %q as root, want the queue left alone", got)
	}
}

func TestOnWindowsRemoveLeavesWhatARunningProgramHoldsToAHiddenCommand(t *testing.T) {
	const (
		local   = `C:\Users\me\AppData\Local`
		folder  = local + `\Programs\printpipe`
		logFile = local + `\printpipe\agent.log`
		config  = `C:\Users\me\AppData\Roaming\printpipe\config.json`
	)
	sys := onWindows()
	sys.files[windowsExe] = []byte("installed")
	sys.files[config] = []byte(`{"origins": ["https://app.example"], "device": "usb:1FC9:2016"}`)
	sys.files[logFile] = []byte("log")
	sys.runValues["printpipe"] = `"` + windowsExe + `" start`
	sys.runValues["Other"] = "other.exe"
	sys.userPath = `C:\Tools;` + strings.ToUpper(folder) + `\;%USERPROFILE%\bin`
	// As on Windows, this command cannot delete the file it runs from.
	sys.removeErr = map[string]error{windowsExe: errors.New("Access is denied.")}
	runWindowsAgent(sys)

	wantOutput(t, run(t, sys, "remove"), "Agent stopped\nprintpipe removed\n")

	if sys.onPort != nil {
		t.Error("the agent still answers")
	}
	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
	if _, ok := sys.runValues["printpipe"]; ok {
		t.Error("the Run value is still there")
	}
	if _, ok := sys.runValues["Other"]; !ok {
		t.Error("another program's Run value was deleted")
	}
	if want := `C:\Tools;%USERPROFILE%\bin`; sys.userPath != want {
		t.Errorf("Path %q, want %q", sys.userPath, want)
	}
	wantGone(t, sys, config, logFile)
	wantLater := []string{logFile, logFile + ".1", folder + `\.printpipe.new`, folder + `\.printpipe.old`, windowsExe}
	if !slices.Equal(slices.Sorted(slices.Values(sys.removedLater)), slices.Sorted(slices.Values(wantLater))) {
		t.Errorf("left to the hidden command %q, want %q", sys.removedLater, wantLater)
	}
	if want := []string{folder, local + `\printpipe`}; !slices.Equal(sys.foldersLater, want) {
		t.Errorf("folders left to the hidden command %q, want %q", sys.foldersLater, want)
	}
}

func TestOnWindowsRemoveQueueSaysNoneWasCreatedAndSucceeds(t *testing.T) {
	sys := onWindows()
	sys.files[`C:\Users\me\AppData\Roaming\printpipe\config.json`] = []byte(`{"origins": ["https://app.example"], "queue": "Some Printer"}`)

	wantOutput(t, run(t, sys, "remove", "--queue"), `printpipe creates no printer queue on Windows; none was deleted
not running
printpipe removed
`)
	if len(sys.ran) != 0 {
		t.Errorf("ran %q", sys.ranCommands())
	}
}

func TestOnWindowsRemoveLeavesAPathWithoutItsFolderAsItIs(t *testing.T) {
	sys := onWindows()
	sys.userPath = `C:\Tools;;C:\Programs\printpipe-other`
	sys.userPathErr = errors.New("must not be written")

	wantOutput(t, run(t, sys, "remove"), "not running\nprintpipe removed\n")
}

func TestOnWindowsRemoveThatCannotStartTheHiddenCommandSaysSo(t *testing.T) {
	sys := onWindows()
	sys.removeLaterErr = errors.New("cmd.exe: not found")

	// The Path no longer has the folder, so the command needs its whole path.
	wantFailure(t, run(t, sys, "remove"), `error: could not remove `+windowsExe+`: cmd.exe: not found
  Run the same command again once that is fixed: "`+windowsExe+`" remove
`)
}

func TestOnMacOSRemoveBootsTheServiceOutAndRemovesBothPlists(t *testing.T) {
	const (
		macConfig = "/Users/me/Library/Application Support/printpipe/config.json"
		macLog    = "/Users/me/Library/Logs/printpipe.log"
	)
	sys, l := onMac()
	sys.files[macExe] = []byte("installed")
	sys.files[macConfig] = []byte(`{"origins": ["https://app.example"]}`)
	sys.files[macLog] = []byte("log")
	sys.files[loginPlistPath] = []byte("plist")
	l.runService()

	wantOutput(t, run(t, sys, "remove"), "Agent stopped\nprintpipe removed\n")

	if l.loaded {
		t.Error("launchd still has the service")
	}
	wantRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
	wantGone(t, sys, macExe, macConfig, macLog, plistPath, loginPlistPath)
	// ~/Library/Logs and the folder of the binary are not printpipe's.
	for _, folder := range []string{"/Users/me/Library/Logs", "/Users/me/.local/bin"} {
		if slices.Contains(sys.emptied, folder) {
			t.Errorf("removed the folder %s", folder)
		}
	}
	if !slices.Contains(sys.emptied, "/Users/me/Library/Application Support/printpipe") {
		t.Errorf("removed the folders %q, want printpipe's", sys.emptied)
	}
}

func TestWithoutAManagerRemoveEndsTheChildAndRemovesTheAutostartEntryAndAnOldUnit(t *testing.T) {
	sys := noManager()
	sys.files[installedExe] = []byte("installed")
	sys.files[autostartPath] = []byte("[Desktop Entry]\n")
	sys.files[unitPath] = []byte("[Service]\n")
	sys.files[wantsPath] = []byte("link")
	runStrayAgent(sys)
	sys.onTerminate = func() { sys.onPort = nil }

	wantOutput(t, run(t, sys, "remove"), "Agent stopped\nprintpipe removed\n")

	if !slices.Equal(sys.terminated, []int{agentPID}) {
		t.Errorf("terminated %v, want the agent", sys.terminated)
	}
	wantGone(t, sys, installedExe, autostartPath, unitPath, wantsPath)
}

func TestRemoveRemovesItsOwnFoldersOnceEmptyAndLeavesOtherFiles(t *testing.T) {
	sys, d := withXPQueue()
	sys.files[installedExe] = []byte("installed")
	sys.files[linuxLog] = []byte("log")
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"]}`)
	sys.files["/home/me/.config/printpipe/notes.txt"] = []byte("the user's")
	d.runUnit()

	wantOutput(t, run(t, sys, "remove"), "Agent stopped\nprintpipe removed\n")

	if !slices.Contains(sys.emptied, "/home/me/.local/state/printpipe") {
		t.Errorf("removed the folders %q, want the log's", sys.emptied)
	}
	if slices.Contains(sys.emptied, "/home/me/.config/printpipe") {
		t.Error("removed the config folder, which holds a file of the user's")
	}
	if _, ok := sys.files["/home/me/.config/printpipe/notes.txt"]; !ok {
		t.Error("removed a file of the user's")
	}
	if slices.Contains(sys.emptied, "/home/me/.local/bin") {
		t.Error("removed ~/.local/bin, which is not printpipe's")
	}
}

func TestRemoveRunAgainSucceeds(t *testing.T) {
	macOS := func() *fakeSystem { sys, _ := onMac(); return sys }
	systemd := func() *fakeSystem { sys := newFakeSystem(); withSystemd(sys); return sys }
	for name, computer := range map[string]func() *fakeSystem{
		"linux with systemd": systemd, "linux without a manager": noManager, "macos": macOS, "windows": onWindows,
	} {
		t.Run(name, func(t *testing.T) {
			sys := computer()
			for range 2 {
				wantOutput(t, run(t, sys, "remove"), "not running\nprintpipe removed\n")
			}
		})
	}
}

func TestRemoveQueueRunAgainSucceedsWithoutAPassword(t *testing.T) {
	sys, _, _ := withCreatedQueue()
	if got := run(t, sys, "remove", "--queue"); got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}
	sys.ran = nil

	wantOutput(t, run(t, sys, "remove", "--queue"), `printpipe created no printer queue here; none was deleted
not running
printpipe removed
`)
	if got := sudoCommands(sys); got != nil {
		t.Errorf("ran %q as root", got)
	}
}

func TestRemoveLeavesAnotherProgramOnThePortAlone(t *testing.T) {
	sys, _ := withXPQueue()
	sys.files[installedExe] = []byte("installed")
	sys.runForeignProgram()

	wantOutput(t, run(t, sys, "remove"), "not running\nprintpipe removed\n")

	if sys.onPort == nil || len(sys.terminated) != 0 || len(sys.killed) != 0 {
		t.Errorf("the other program was touched: terminated %v, killed %v", sys.terminated, sys.killed)
	}
	wantGone(t, sys, installedExe)
}

// A process that answers as printpipe but runs another file is not this one's to
// stop, and removing the rest would leave it running with nothing to stop it by.
func TestRemoveChangesNothingWhenTheAgentOnThePortIsNotItsOwn(t *testing.T) {
	sys, _, queues := withCreatedQueue()
	sys.exes[agentPID] = "/opt/other/printpipe"

	got := run(t, sys, "remove", "--queue")

	wantFailure(t, got, "error: process 4242 on port 8631 runs /opt/other/printpipe, not this printpipe\n"+
		"  Close that program and run the same command again.\n")
	if _, ok := queues["XP-365B"]; !ok {
		t.Error("the queue was deleted")
	}
	if _, ok := sys.files[configPath]; !ok {
		t.Error("config.json was removed")
	}
}

// The binary goes last, so that what is left can run the same command again, and the
// config just before it, so that the command still knows the queue.
func TestRemoveThatCannotRemoveAFileKeepsTheBinaryAndTheConfig(t *testing.T) {
	sys, d, _ := withCreatedQueue()
	sys.files[linuxLog] = []byte("log")
	sys.removeErr = map[string]error{linuxLog: errors.New("permission denied")}
	d.runUnit()

	got := run(t, sys, "remove")

	wantFailure(t, got, "error: could not remove "+linuxLog+": permission denied\n"+
		"  Run the same command again once that is fixed.\n")
	for _, file := range []string{installedExe, configPath} {
		if _, ok := sys.files[file]; !ok {
			t.Errorf("%s was removed", file)
		}
	}

	sys.removeErr = nil
	wantOutput(t, run(t, sys, "remove"), `not running
printpipe removed
Printer queue kept: XP-365B. To delete it: sudo lpadmin -x XP-365B
`)
	wantGone(t, sys, installedExe, linuxLog, configPath)
}

// The unit file is gone by then, and the manager still has to forget it.
func TestRemoveRunAgainReloadsAManagerThatFailedToReload(t *testing.T) {
	sys, d := withXPQueue()
	d.runUnit()
	systemctl := sys.programs["systemctl"]
	sys.programs["systemctl"] = func(args []string) ([]byte, error) {
		if args[1] == "daemon-reload" {
			return nil, errors.New("systemctl: exit status 1: Failed to reload")
		}
		return systemctl(args)
	}
	if got := run(t, sys, "remove"); got.code != 1 {
		t.Fatalf("got %+v, want exit 1", got)
	}
	sys.programs["systemctl"] = systemctl
	sys.ran = nil

	wantOutput(t, run(t, sys, "remove"), "not running\nprintpipe removed\n")
	wantRan(t, sys, "systemctl --user daemon-reload")
}

func TestRemoveWithAConfigItCannotReadSaysWhatToDoAndChangesNothing(t *testing.T) {
	sys, d := withXPQueue()
	sys.files[installedExe] = []byte("installed")
	sys.files[configPath] = []byte("{not json")
	d.runUnit()

	got := run(t, sys, "remove")

	if got.code != 1 || !strings.HasPrefix(got.stderr, "error: "+configPath+": ") ||
		!strings.HasSuffix(got.stderr, "\n  Correct or delete that file and run the same command again.\n") {
		t.Errorf("got %+v, want exit 1, the file and the hint", got)
	}
	if _, ok := sys.files[installedExe]; !ok {
		t.Error("the binary was removed")
	}
	if sys.onPort == nil {
		t.Error("the agent was stopped")
	}
}

func TestRemoveFromACopyRemovesTheInstalledBinaryAndNotTheCopy(t *testing.T) {
	sys, _ := withXPQueue()
	sys.executable = "/home/me/Downloads/printpipe"
	sys.files[sys.executable] = []byte("copy")
	sys.files[installedExe] = []byte("installed")

	wantOutput(t, run(t, sys, "remove"), "not running\nprintpipe removed\n")

	wantGone(t, sys, installedExe)
	if _, ok := sys.files[sys.executable]; !ok {
		t.Error("the copy was removed")
	}
}

func TestRemoveWrongUsageExitsWithTwoAndRemovesNothing(t *testing.T) {
	for _, args := range [][]string{{"remove", "now"}, {"remove", "--queue=XP-365B"}, {"remove", "--force"}} {
		sys, _ := withXPQueue()
		sys.files[installedExe] = []byte("installed")
		if got := run(t, sys, args...); got.code != 2 {
			t.Errorf("%q: got %+v, want exit 2", args, got)
		}
		if _, ok := sys.files[installedExe]; !ok {
			t.Errorf("%q: the binary was removed", args)
		}
	}
}
