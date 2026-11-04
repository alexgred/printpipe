//go:build !windows

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	releases = "https://github.com/alexgred/printpipe/releases"
	newExe   = "/home/me/.local/bin/.printpipe.new"
	oldExe   = "/home/me/.local/bin/.printpipe.old"
	logFile  = "/home/me/.local/state/printpipe/agent.log"
)

// installedAs makes the running printpipe the installed binary of this version.
func installedAs(t *testing.T, sys *fakeSystem, version string) {
	t.Helper()
	old := Version
	Version = version
	t.Cleanup(func() { Version = old })
	sys.files[binaryOf(t, sys)] = []byte("old build")
}

func binaryOf(t *testing.T, sys *fakeSystem) string {
	t.Helper()
	files, err := filesOf(sys)
	if err != nil {
		t.Fatal(err)
	}
	return files.Binary
}

// wantNothingDownloaded is a check for a release that stopped at its tag.
func wantNothingDownloaded(t *testing.T, sys *fakeSystem) {
	t.Helper()
	for _, url := range sys.fetched {
		if strings.Contains(url, "/download/") {
			t.Errorf("downloaded %s", url)
		}
	}
}

// latestIs makes tag the latest release, as GitHub redirects to it.
func latestIs(sys *fakeSystem, tag string) {
	sys.redirects = map[string]string{releases + "/latest": releases + "/tag/" + tag}
}

func TestUpgradeOnTheLatestVersionDoesNothing(t *testing.T) {
	sys := noManager()
	installedAs(t, sys, "v0.3.0")
	latestIs(sys, "v0.3.0")

	wantOutput(t, run(t, sys, "upgrade"), "printpipe v0.3.0 is the latest version\n")
	wantNothingDownloaded(t, sys)
}

// The order is semver's, not the text's: v0.10.0 comes after v0.9.0.
func TestUpgradeWithANewerInstalledVersionDoesNothing(t *testing.T) {
	for _, tc := range []struct{ installed, latest string }{
		{"v0.10.0", "v0.9.0"},
		{"v1.0.0", "v0.99.99"},
		{"v0.3.0", "v0.3.0-rc.1"},
		{"v0.3.0-rc.2", "v0.3.0-rc.1"},
		{"v0.3.0-rc.10", "v0.3.0-rc.9"},
		{"v0.3.0-rc.1", "v0.3.0-rc"},
		{"v0.3.0-rc", "v0.3.0-1"},
	} {
		t.Run(tc.installed+" over "+tc.latest, func(t *testing.T) {
			sys := noManager()
			installedAs(t, sys, tc.installed)
			latestIs(sys, tc.latest)

			wantOutput(t, run(t, sys, "upgrade"), "installed "+tc.installed+" is newer than the latest release "+tc.latest+"; nothing to do\n")
			wantNothingDownloaded(t, sys)
		})
	}
}

func TestUpgradeWithoutTheNetworkSaysSo(t *testing.T) {
	sys := noManager()
	installedAs(t, sys, "v0.2.0")
	sys.offline = errors.New("dial tcp: no route to host")

	wantFailure(t, run(t, sys, "upgrade"), "error: could not reach GitHub\n"+
		"  Check the internet connection and run printpipe upgrade again.\n")
}

func TestUpgradeWithNoLatestReleaseSaysSo(t *testing.T) {
	for _, tc := range []struct{ name, location, stderr string }{
		{"no release yet", releases, "error: could not find the latest release on GitHub: it answered with " + releases + "\n"},
		{"not a version", releases + "/tag/nightly", "error: could not find the latest release on GitHub: it answered with " + releases + "/tag/nightly\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := noManager()
			installedAs(t, sys, "v0.2.0")
			sys.redirects = map[string]string{releases + "/latest": tc.location}

			wantFailure(t, run(t, sys, "upgrade"), tc.stderr)
		})
	}
	t.Run("no redirect", func(t *testing.T) {
		sys := noManager()
		installedAs(t, sys, "v0.2.0")

		wantFailure(t, run(t, sys, "upgrade"), "error: could not find the latest release on GitHub: HTTP 404\n")
	})
}

// No flag changes what upgrade does.
func TestUpgradeTakesNoFlags(t *testing.T) {
	for _, flag := range []string{"--force", "--version=v0.1.0", "--check"} {
		if got := run(t, noManager(), "upgrade", flag); got.code != 2 {
			t.Errorf("%s: got %+v, want exit 2", flag, got)
		}
	}
}

func TestUpgradeFromACopyThatIsNotInstalledIsRefused(t *testing.T) {
	t.Run("with one installed", func(t *testing.T) {
		sys := noManager()
		installedAs(t, sys, "v0.2.0")
		downloadedCopy(sys)
		latestIs(sys, "v0.3.0")

		wantFailure(t, run(t, sys, "upgrade"), "error: this copy of printpipe is not the installed one, "+installedExe+"\n"+
			"  Upgrade the installed one: "+installedExe+" upgrade\n")
		if len(sys.fetched) != 0 {
			t.Errorf("asked the web for %q", sys.fetched)
		}
	})
	t.Run("with none installed", func(t *testing.T) {
		sys := noManager()
		downloadedCopy(sys)
		latestIs(sys, "v0.3.0")

		wantFailure(t, run(t, sys, "upgrade"), "error: printpipe is not installed\n"+
			"  Install it: printpipe install --origin <url>\n")
	})
}

// released is a computer where printpipe v0.2.0 is installed, its config in place,
// and v0.3.0 is the latest release, for Linux, with a build that runs.
func released(t *testing.T, sys *fakeSystem) {
	t.Helper()
	installedAs(t, sys, "v0.2.0")
	sys.files[configPath] = []byte(`{"origins": ["` + app + `"], "queue": "XP-365B"}`)
	publish(t, sys, "printpipe-linux-amd64", "  printpipe-linux-amd64\n", "\n")
}

// publish makes v0.3.0 the latest release: asset is this computer's build, listed
// is what follows its hash in SHA256SUMS, and lineEnd what ends its version.
func publish(t *testing.T, sys *fakeSystem, asset, listed, lineEnd string) {
	t.Helper()
	latestIs(sys, "v0.3.0")
	sum := sha256.Sum256([]byte("new build"))
	sys.downloads = map[string][]byte{
		releases + "/download/v0.3.0/" + asset: []byte("new build"),
		releases + "/download/v0.3.0/SHA256SUMS": []byte("0000  printpipe-other-amd64\n" +
			hex.EncodeToString(sum[:]) + listed),
	}
	files, err := filesOf(sys)
	if err != nil {
		t.Fatal(err)
	}
	sys.programs[files.NewBinary] = func(args []string) ([]byte, error) {
		if !slices.Equal(args, []string{"version"}) {
			return nil, errors.New("not asked for its version")
		}
		return []byte("printpipe v0.3.0" + lineEnd), nil
	}
	sys.buildVersions = map[string]string{"old build": "v0.2.0", "new build": "v0.3.0"}
}

// wantNoSwapFiles is the folder of the binary with no file of a swap left in it.
func wantNoSwapFiles(t *testing.T, sys *fakeSystem) {
	t.Helper()
	files, err := filesOf(sys)
	if err != nil {
		t.Fatal(err)
	}
	for _, leftover := range []string{files.NewBinary, files.OldBinary} {
		if _, there := sys.files[leftover]; there {
			t.Errorf("%s is left", leftover)
		}
	}
}

// wantNothingInstalled is the state a failure before the swap leaves.
func wantNothingInstalled(t *testing.T, sys *fakeSystem) {
	t.Helper()
	if got := string(sys.files[binaryOf(t, sys)]); got != "old build" {
		t.Errorf("the installed binary is %q", got)
	}
	wantNoSwapFiles(t, sys)
}

func TestUpgradeWithTheAgentStoppedReplacesTheBinaryAndStartsNothing(t *testing.T) {
	sys := noManager()
	released(t, sys)
	sys.files[newExe] = []byte("left by a failed try")
	sys.files[oldExe] = []byte("left by an earlier swap")
	config := string(sys.files[configPath])

	wantOutput(t, run(t, sys, "upgrade"), "Downloading printpipe v0.3.0\n"+
		"updated to v0.3.0; the agent is not running, it will use the new version when started\n")

	if string(sys.files[installedExe]) != "new build" {
		t.Errorf("the installed binary is %q", sys.files[installedExe])
	}
	wantNoSwapFiles(t, sys)
	if !slices.Contains(sys.installedPrograms, newExe) {
		t.Errorf("wrote as programs %q, want the download, runnable", sys.installedPrograms)
	}
	if len(sys.spawned) != 0 || sys.onPort != nil {
		t.Errorf("started an agent: %q", sys.spawned)
	}
	if string(sys.files[configPath]) != config {
		t.Errorf("config.json changed:\n%s", sys.files[configPath])
	}
}

// Only the file for this OS and processor is fetched, and it is named by the tag.
func TestUpgradeFetchesThisComputersBuildFromTheTag(t *testing.T) {
	sys := noManager()
	released(t, sys)

	wantOutputCode(t, run(t, sys, "upgrade"), 0)
	want := []string{
		releases + "/download/v0.3.0/SHA256SUMS",
		releases + "/download/v0.3.0/printpipe-linux-amd64",
		releases + "/latest",
	}
	slices.Sort(sys.fetched)
	if !slices.Equal(sys.fetched, want) {
		t.Errorf("fetched %q, want %q", sys.fetched, want)
	}
}

// A build that is no semver is a development build: any release is newer.
func TestUpgradeFromADevelopmentBuildInstallsTheRelease(t *testing.T) {
	for _, version := range []string{"dev", "abc1234"} {
		t.Run(version, func(t *testing.T) {
			sys := noManager()
			released(t, sys)
			Version = version

			wantOutputCode(t, run(t, sys, "upgrade"), 0)
			if string(sys.files[installedExe]) != "new build" {
				t.Errorf("the installed binary is %q", sys.files[installedExe])
			}
		})
	}
}

func TestUpgradeThatFailsBeforeTheSwapLeavesEverythingAsItWas(t *testing.T) {
	const binaryURL = releases + "/download/v0.3.0/printpipe-linux-amd64"
	const sumsURL = releases + "/download/v0.3.0/SHA256SUMS"
	for _, tc := range []struct {
		name   string
		breaks func(sys *fakeSystem)
		stderr string
	}{
		{
			"the connection goes during the download",
			func(sys *fakeSystem) { sys.downloadErr = errors.New("connection reset") },
			"error: could not reach GitHub\n  Check the internet connection and run printpipe upgrade again.\n",
		},
		{
			"the release has no build for this computer",
			func(sys *fakeSystem) { delete(sys.downloads, binaryURL) },
			"error: could not download printpipe-linux-amd64 of v0.3.0: HTTP 404\n",
		},
		{
			"the release has no checksums",
			func(sys *fakeSystem) { delete(sys.downloads, sumsURL) },
			"error: could not download SHA256SUMS of v0.3.0: HTTP 404\n",
		},
		{
			"the download is damaged",
			func(sys *fakeSystem) { sys.downloads[binaryURL] = []byte("damaged") },
			"error: the download does not match SHA256SUMS: it is damaged or was tampered with\n  Nothing was installed.\n",
		},
		{
			"the checksums do not list the build",
			func(sys *fakeSystem) { sys.downloads[sumsURL] = []byte("0000  printpipe-darwin-arm64\n") },
			"error: SHA256SUMS does not list printpipe-linux-amd64\n  Nothing was installed.\n",
		},
		{
			"the download does not run",
			func(sys *fakeSystem) {
				sys.programs[newExe] = func([]string) ([]byte, error) { return nil, errors.New("exec format error") }
			},
			"error: the downloaded printpipe does not run: exec format error\n",
		},
		{
			"the download says it is another version",
			func(sys *fakeSystem) {
				sys.programs[newExe] = func([]string) ([]byte, error) { return []byte("printpipe v0.2.9\n"), nil }
			},
			"error: the downloaded printpipe says it is \"printpipe v0.2.9\", not v0.3.0\n",
		},
		{
			"the swap fails",
			func(sys *fakeSystem) { sys.renameErr[installedExe] = errors.New("permission denied") },
			"error: could not replace " + installedExe + ": permission denied\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := noManager()
			released(t, sys)
			tc.breaks(sys)
			sys.files[newExe] = []byte("left by a failed try")

			wantFailure(t, run(t, sys, "upgrade"), tc.stderr)
			wantNothingInstalled(t, sys)
		})
	}
}

// An agent running through a failed upgrade is not touched.
func TestUpgradeThatFailsBeforeTheSwapLeavesARunningAgentAlone(t *testing.T) {
	sys, d := withXPQueue()
	released(t, sys)
	d.runUnit()
	delete(sys.downloads, releases+"/download/v0.3.0/SHA256SUMS")

	wantOutputCode(t, run(t, sys, "upgrade"), 1)
	wantNothingInstalled(t, sys)
	wantNotRan(t, sys, "systemctl --user restart printpipe.service")
	wantNotRan(t, sys, "systemctl --user stop printpipe.service")
}

// releasedOnWindows is released for Windows, whose build carries its extension in
// the release and in the checksums, and says its version with a Windows line end.
// A started agent runs what is installed, and exits when asked to.
func releasedOnWindows(t *testing.T) (sys *fakeSystem, exe string) {
	t.Helper()
	sys = newFakeSystem()
	sys.goos = "windows"
	sys.configDir = sys.home + `\AppData\Roaming`
	exe = sys.home + `\AppData\Local\Programs\printpipe\printpipe.exe`
	sys.executable = exe
	installedAs(t, sys, "v0.2.0")
	publish(t, sys, "printpipe-windows-amd64.exe", " *printpipe-windows-amd64.exe\n", "\r\n")
	sys.onTerminate = func() { sys.onPort = nil }
	sys.onSpawn = func() {
		sys.runAgent(&fakePrinter{})
		sys.exes[agentPID] = exe
	}
	return sys, exe
}

func TestUpgradeOnWindowsFetchesTheExe(t *testing.T) {
	sys, exe := releasedOnWindows(t)

	got := run(t, sys, "upgrade")

	if got.code != 0 || !strings.HasSuffix(got.stdout, "the agent is not running, it will use the new version when started\n") {
		t.Errorf("got %+v", got)
	}
	if string(sys.files[exe]) != "new build" {
		t.Errorf("the installed binary is %q", sys.files[exe])
	}
}

// agentVersion is the version status says the agent runs, "" with none running.
func agentVersion(t *testing.T, sys *fakeSystem) string {
	t.Helper()
	match := regexp.MustCompile(`, running (\S+)`).FindStringSubmatch(run(t, sys, "status").stdout)
	if match == nil {
		return ""
	}
	return match[1]
}

// wantRolledBack is the state a rollback leaves: the old binary, in its place, and
// its agent running.
func wantRolledBack(t *testing.T, sys *fakeSystem) {
	t.Helper()
	wantNothingInstalled(t, sys)
	if v := agentVersion(t, sys); v != "v0.2.0" {
		t.Errorf("the agent runs %q, want v0.2.0", v)
	}
}

// onSystemctl runs hook on one systemctl command, before the manager gets it.
func onSystemctl(sys *fakeSystem, command string, hook func() error) {
	manager := sys.programs["systemctl"]
	sys.programs["systemctl"] = func(args []string) ([]byte, error) {
		if args[1] == command {
			if err := hook(); err != nil {
				return nil, err
			}
		}
		return manager(args)
	}
}

func TestUpgradeRestartsARunningAgentThroughItsManager(t *testing.T) {
	sys, d := withXPQueue()
	released(t, sys)
	d.runUnit()

	wantOutput(t, run(t, sys, "upgrade"), "Downloading printpipe v0.3.0\nupdated to v0.3.0; agent restarted\n")

	wantRan(t, sys, "systemctl --user restart printpipe.service")
	if v := agentVersion(t, sys); v != "v0.3.0" {
		t.Errorf("the agent runs %q", v)
	}
	if _, there := sys.files[oldExe]; there {
		t.Error("the old binary is left")
	}
	if len(d.printer.jobs) != 0 {
		t.Errorf("printed %d labels", len(d.printer.jobs))
	}
}

func TestUpgradeRestartsAnAgentWithNoManagerFromTheInstalledBinary(t *testing.T) {
	sys := noManager()
	released(t, sys)
	runStrayAgent(sys)

	wantOutputCode(t, run(t, sys, "upgrade"), 0)

	if !slices.Equal(sys.spawned, []string{installedExe + " serve"}) {
		t.Errorf("spawned %q", sys.spawned)
	}
	if v := agentVersion(t, sys); v != "v0.3.0" {
		t.Errorf("the agent runs %q", v)
	}
}

// Another program on the port is no agent of this user's: it is not stopped, and
// the binary still is the user's to update.
func TestUpgradeLeavesAnotherProgramOnThePortAlone(t *testing.T) {
	sys := noManager()
	released(t, sys)
	sys.runForeignProgram()

	got := run(t, sys, "upgrade")

	if got.code != 0 || !strings.Contains(got.stdout, "the agent is not running") {
		t.Errorf("got %+v", got)
	}
	if len(sys.terminated)+len(sys.killed)+len(sys.spawned) != 0 {
		t.Errorf("touched a process: %v %v %q", sys.terminated, sys.killed, sys.spawned)
	}
}

// An agent run from another copy could not be restarted, so nothing starts.
func TestUpgradeWithAnAgentOfAnotherCopyChangesNothing(t *testing.T) {
	sys := noManager()
	released(t, sys)
	runStrayAgent(sys)
	sys.exes[agentPID] = "/home/me/Downloads/printpipe"

	wantFailure(t, run(t, sys, "upgrade"), "error: process 4242 on port 8631 runs /home/me/Downloads/printpipe, not this printpipe\n"+
		"  Close that program and run the same command again.\n")

	wantNothingInstalled(t, sys)
	wantNothingDownloaded(t, sys)
	if v := agentVersion(t, sys); v != "v0.2.0" {
		t.Errorf("the agent runs %q", v)
	}
}

func TestUpgradeRollsBackANewVersionThatDoesNotComeUp(t *testing.T) {
	const log = "error: rolled back to v0.2.0, log: " + logFile + "\n"
	const silent = "  v0.3.0 did not come up: it did not answer as v0.3.0 within 10 s\n"

	t.Run("under a manager", func(t *testing.T) {
		sys, d := withXPQueue()
		released(t, sys)
		d.runUnit()
		sys.buildVersions["new build"] = ""

		wantFailure(t, run(t, sys, "upgrade"), log+silent)
		wantRolledBack(t, sys)
		wantRan(t, sys, "systemctl --user stop printpipe.service")
		wantRan(t, sys, "systemctl --user start printpipe.service")
	})
	t.Run("with no manager", func(t *testing.T) {
		sys := noManager()
		released(t, sys)
		runStrayAgent(sys)
		sys.buildVersions["new build"] = ""

		wantFailure(t, run(t, sys, "upgrade"), log+silent)
		wantRolledBack(t, sys)
	})
	t.Run("up as another version", func(t *testing.T) {
		sys, d := withXPQueue()
		released(t, sys)
		d.runUnit()
		sys.buildVersions["new build"] = "v0.2.9"

		wantFailure(t, run(t, sys, "upgrade"), log+silent)
		wantRolledBack(t, sys)
		wantRan(t, sys, "systemctl --user stop printpipe.service")
	})
	t.Run("the manager refuses", func(t *testing.T) {
		sys, d := withXPQueue()
		released(t, sys)
		d.runUnit()
		onSystemctl(sys, "restart", func() error { return errors.New("systemctl: exit status 1") })

		wantFailure(t, run(t, sys, "upgrade"), log+"  v0.3.0 did not come up: systemctl: exit status 1\n")
		wantRolledBack(t, sys)
	})
}

// Ctrl+C during the wait must not leave the new binary in place of a working one.
func TestUpgradeInterruptedDuringTheRestartStillRollsBack(t *testing.T) {
	sys, d := withXPQueue()
	released(t, sys)
	d.runUnit()
	sys.buildVersions["new build"] = ""
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	onSystemctl(sys, "restart", func() error { cancel(); return nil })

	got := runCtx(ctx, sys, "upgrade")

	if got.code != 1 || !strings.HasPrefix(got.stderr, "error: rolled back to v0.2.0, log: "+logFile+"\n") {
		t.Errorf("got %+v", got)
	}
	wantRolledBack(t, sys)
}

func TestUpgradeSaysWhenTheRolledBackAgentDoesNotComeBack(t *testing.T) {
	sys, d := withXPQueue()
	released(t, sys)
	d.runUnit()
	sys.buildVersions["new build"] = ""
	onSystemctl(sys, "restart", func() error { sys.buildVersions["old build"] = ""; return nil })

	wantFailure(t, run(t, sys, "upgrade"), "error: rolled back to v0.2.0, log: "+logFile+"\n"+
		"  v0.3.0 did not come up: it did not answer as v0.3.0 within 10 s\n"+
		"  The agent did not come back: printpipe start\n")
	wantNothingInstalled(t, sys)
}

func TestUpgradeSaysWhenTheRollbackItselfFails(t *testing.T) {
	sys, d := withXPQueue()
	released(t, sys)
	d.runUnit()
	sys.buildVersions["new build"] = ""
	onSystemctl(sys, "restart", func() error {
		sys.renameErr[installedExe] = errors.New("permission denied")
		return nil
	})

	wantFailure(t, run(t, sys, "upgrade"), "error: could not roll back to v0.2.0: permission denied\n"+
		"  v0.3.0 did not come up: it did not answer as v0.3.0 within 10 s\n"+
		"  Log: "+logFile+"\n")
	if string(sys.files[installedExe]) != "new build" {
		t.Errorf("the installed binary is %q, want a program there still", sys.files[installedExe])
	}
}

// Windows has no manager to restart through: the agent is stopped and spawned.
func TestUpgradeOnWindowsStopsAndSpawnsTheAgent(t *testing.T) {
	sys, exe := releasedOnWindows(t)
	sys.onSpawn()

	wantOutputCode(t, run(t, sys, "upgrade"), 0)

	if !slices.Equal(sys.spawned, []string{exe + " serve"}) {
		t.Errorf("spawned %q", sys.spawned)
	}
	if v := agentVersion(t, sys); v != "v0.3.0" {
		t.Errorf("the agent runs %q", v)
	}
}

func TestUpgradeOnWindowsRollsBack(t *testing.T) {
	sys, exe := releasedOnWindows(t)
	sys.onSpawn()
	sys.buildVersions["new build"] = ""

	got := run(t, sys, "upgrade")

	if got.code != 1 || !strings.HasPrefix(got.stderr, `error: rolled back to v0.2.0, log: `) {
		t.Errorf("got %+v", got)
	}
	if !slices.Equal(sys.spawned, []string{exe + " serve", exe + " serve"}) {
		t.Errorf("spawned %q, want the new agent, then the old", sys.spawned)
	}
	wantRolledBack(t, sys)
}

func TestUpgradeOnAMacRollsBackAndLoadsTheServiceAgain(t *testing.T) {
	sys, l := onMac()
	installedAs(t, sys, "v0.2.0")
	publish(t, sys, "printpipe-darwin-amd64", "  printpipe-darwin-amd64\n", "\n")
	l.runService()
	sys.buildVersions["new build"] = ""

	got := run(t, sys, "upgrade")

	if got.code != 1 || !strings.HasPrefix(got.stderr, "error: rolled back to v0.2.0, log: ") {
		t.Errorf("got %+v", got)
	}
	wantRan(t, sys, "launchctl bootout "+launchdDomain+" "+plistPath)
	wantRolledBack(t, sys)
}

func TestUpgradeOnAMacRestartsTheServiceAndClearsTheQuarantineMark(t *testing.T) {
	sys, l := onMac()
	installedAs(t, sys, "v0.2.0")
	publish(t, sys, "printpipe-darwin-amd64", "  printpipe-darwin-amd64\n", "\n")
	l.runService()
	sys.programs["xattr"] = func([]string) ([]byte, error) { return nil, nil }

	wantOutputCode(t, run(t, sys, "upgrade"), 0)

	wantRan(t, sys, "launchctl kickstart -k "+launchdService)
	wantRan(t, sys, "xattr -d com.apple.quarantine /Users/me/.local/bin/.printpipe.new")
	if v := agentVersion(t, sys); v != "v0.3.0" {
		t.Errorf("the agent runs %q", v)
	}
}

// afterSystemctl changes the world once the manager has done one command.
func afterSystemctl(sys *fakeSystem, command string, then func()) {
	manager := sys.programs["systemctl"]
	sys.programs["systemctl"] = func(args []string) ([]byte, error) {
		out, err := manager(args)
		if args[1] == command {
			then()
		}
		return out, err
	}
}

// An agent on its way down may answer anything for a moment, and a good release
// is not rolled back for it.
func TestUpgradeWaitsThroughAnOddAnswerDuringTheRestart(t *testing.T) {
	sys, d := withXPQueue()
	released(t, sys)
	d.runUnit()
	afterSystemctl(sys, "restart", func() {
		agent, odd := sys.onPort, true
		sys.onPort = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if odd {
				odd = false
				foreignProgram.ServeHTTP(w, r)
				return
			}
			agent.ServeHTTP(w, r)
		})
	})

	wantOutputCode(t, run(t, sys, "upgrade"), 0)
	if string(sys.files[installedExe]) != "new build" {
		t.Errorf("the installed binary is %q", sys.files[installedExe])
	}
}

// A new build that runs without a usable answer is still running, and starting a
// unit that is active does nothing: it has to be stopped first.
func TestUpgradeStopsANewBuildThatDoesNotAnswerBeforeRollingBack(t *testing.T) {
	sys, d := withXPQueue()
	released(t, sys)
	d.runUnit()
	sys.buildVersions["new build"] = ""
	afterSystemctl(sys, "restart", func() { sys.onPort = foreignProgram })

	wantOutputCode(t, run(t, sys, "upgrade"), 1)

	wantRan(t, sys, "systemctl --user stop printpipe.service")
	wantRolledBack(t, sys)
}
