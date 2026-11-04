//go:build !windows

package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	testOrigin   = "https://app.example"
	linuxBuild   = "printpipe-linux-amd64"
	downloadBase = "https://github.com/alexgred/printpipe/releases/latest/download/"
)

// fakeBuild stands in for a release's binary: it says it ran and how, and exits
// with what the file beside its record says.
const fakeBuild = `#!/bin/sh
printf '%s\n' "$@" > "$RECORD/args"
exit "$(cat "$RECORD/exit")"
`

// fakeCurl serves the release from a folder and notes each URL. curl's exit code
// comes from a file: without one, a file not in the release is curl -f's 22.
const fakeCurl = `#!/bin/sh
while [ $# -gt 1 ]; do
	[ "$1" = -o ] && { shift; out=$1; }
	shift
done
printf '%s\n' "$1" >> "$RECORD/urls"
[ -f "$RECORD/curl-exit" ] && exit "$(cat "$RECORD/curl-exit")"
cat "$RELEASE/${1##*/}" > "$out" 2>/dev/null || exit 22
`

// fakeUname answers what the computer under test is.
const fakeUname = `#!/bin/sh
case $1 in
-s) echo "$FAKE_OS" ;;
-m) echo "$FAKE_ARCH" ;;
esac
`

// fakeShasum is macOS's checksum command, the only one a Mac may have.
const fakeShasum = `#!/bin/sh
[ "$1" = -a ] && [ "$2" = 256 ] || exit 64
exec "$REAL_SHA256SUM" "$3"
`

// computer is the world install.sh runs in, as one test sets it up.
type computer struct {
	os, arch  string
	release   map[string][]byte // the release's files
	buildExit int               // what the build exits with
	curlExit  int               // what curl exits with; 0 serves the release
	noSHA256  bool              // has shasum instead of sha256sum, as a Mac
	noTemp    bool              // the temp folder cannot be used
	script    []byte            // install.sh by default
}

// ran is what is seen once install.sh is done.
type ran struct {
	code      int
	stderr    string
	buildArgs []string // nil when the build did not run
	urls      []string
	leftovers []string // what stayed in the temp folder
}

// linuxRelease is a release whose linux/amd64 build matches SHA256SUMS.
func linuxRelease() map[string][]byte {
	return map[string][]byte{
		linuxBuild:   []byte(fakeBuild),
		"SHA256SUMS": sumsLine([]byte(fakeBuild), linuxBuild),
	}
}

func sumsLine(content []byte, name string) []byte {
	return fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(content), name)
}

// runInstallSh pipes install.sh into sh, as the one-line command does.
func runInstallSh(t *testing.T, c computer, args ...string) ran {
	t.Helper()
	dir := t.TempDir()
	record, release, tools, tmp := filepath.Join(dir, "record"), filepath.Join(dir, "release"), filepath.Join(dir, "tools"), filepath.Join(dir, "tmp")
	for _, d := range []string{record, release, tools, tmp} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(record, "exit"), fmt.Sprint(c.buildExit), 0o644)
	if c.curlExit != 0 {
		write(filepath.Join(record, "curl-exit"), fmt.Sprint(c.curlExit), 0o644)
	}
	if c.release == nil {
		c.release = linuxRelease()
	}
	for name, content := range c.release {
		write(filepath.Join(release, name), string(content), 0o644)
	}

	// Only these commands exist for the script: what a bare macOS or Linux has.
	realSHA256, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("sha256sum is needed for this test")
	}
	for _, tool := range []string{"sh", "awk", "cat", "chmod", "mktemp", "rm"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(tools, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if c.noSHA256 {
		write(filepath.Join(tools, "shasum"), fakeShasum, 0o755)
	} else if err := os.Symlink(realSHA256, filepath.Join(tools, "sha256sum")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(tools, "curl"), fakeCurl, 0o755)
	write(filepath.Join(tools, "uname"), fakeUname, 0o755)

	if c.os == "" {
		c.os, c.arch = "Linux", "x86_64"
	}
	script := c.script
	if script == nil {
		script, err = os.ReadFile("install.sh")
		if err != nil {
			t.Fatal(err)
		}
	}
	tmpdir := tmp
	if c.noTemp {
		tmpdir = filepath.Join(dir, "no-such-folder")
	}
	cmd := exec.Command(filepath.Join(tools, "sh"), append([]string{"-s", "--"}, args...)...)
	cmd.Stdin = bytes.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = []string{
		"PATH=" + tools,
		"TMPDIR=" + tmpdir,
		"RECORD=" + record,
		"RELEASE=" + release,
		"FAKE_OS=" + c.os,
		"FAKE_ARCH=" + c.arch,
		"REAL_SHA256SUM=" + realSHA256,
	}
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}

	r := ran{code: code, stderr: stderr.String()}
	if b, err := os.ReadFile(filepath.Join(record, "args")); err == nil {
		r.buildArgs = strings.Fields(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(record, "urls")); err == nil {
		r.urls = strings.Fields(string(b))
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		r.leftovers = append(r.leftovers, entry.Name())
	}
	return r
}

// tamperedRelease is a release whose linux/amd64 build does not match SHA256SUMS.
func tamperedRelease() map[string][]byte {
	release := linuxRelease()
	release[linuxBuild] = []byte(fakeBuild + "# tampered\n")
	return release
}

// wantRefused is install.sh having failed without running the build.
func wantRefused(t *testing.T, r ran) {
	t.Helper()
	if r.code != 1 || r.buildArgs != nil {
		t.Errorf("exit code %d, build ran with %q; want 1 and not run", r.code, r.buildArgs)
	}
}

func wantNoLeftovers(t *testing.T, r ran) {
	t.Helper()
	if len(r.leftovers) != 0 {
		t.Errorf("left in the temp folder: %q", r.leftovers)
	}
}

func TestInstallShRunsTheVerifiedBuildAndDeletesIt(t *testing.T) {
	r := runInstallSh(t, computer{}, "--origin", testOrigin)

	if r.code != 0 {
		t.Fatalf("exit code %d, stderr: %s", r.code, r.stderr)
	}
	if want := []string{"install", "--origin", testOrigin}; !slices.Equal(r.buildArgs, want) {
		t.Errorf("the build ran with %q, want %q", r.buildArgs, want)
	}
	for _, want := range []string{downloadBase + linuxBuild, downloadBase + "SHA256SUMS"} {
		if !slices.Contains(r.urls, want) {
			t.Errorf("downloaded %q, want %s among them", r.urls, want)
		}
	}
	wantNoLeftovers(t, r)
}

func TestInstallShRefusesABuildThatDoesNotMatch(t *testing.T) {
	r := runInstallSh(t, computer{release: tamperedRelease()}, "--origin", testOrigin)

	wantRefused(t, r)
	want := "error: the download does not match SHA256SUMS: it is damaged or was tampered with\n  Nothing was installed.\n"
	if r.stderr != want {
		t.Errorf("stderr %q, want %q", r.stderr, want)
	}
	wantNoLeftovers(t, r)
}

func TestInstallShRefusesABuildSHA256SUMSDoesNotList(t *testing.T) {
	// A longer name holding the one looked for must not count as listing it.
	release := linuxRelease()
	release["SHA256SUMS"] = sumsLine([]byte(fakeBuild), linuxBuild+".exe")

	r := runInstallSh(t, computer{release: release})

	wantRefused(t, r)
	want := "error: SHA256SUMS does not list " + linuxBuild + "\n  Nothing was installed.\n"
	if r.stderr != want {
		t.Errorf("stderr %q, want %q", r.stderr, want)
	}
}

func TestInstallShPicksTheBuildOfThisComputer(t *testing.T) {
	for _, tc := range []struct{ os, arch, asset string }{
		{"Linux", "x86_64", "printpipe-linux-amd64"},
		{"Darwin", "x86_64", "printpipe-darwin-amd64"},
		{"Darwin", "arm64", "printpipe-darwin-arm64"},
	} {
		t.Run(tc.os+"-"+tc.arch, func(t *testing.T) {
			r := runInstallSh(t, computer{os: tc.os, arch: tc.arch, release: map[string][]byte{
				tc.asset:     []byte(fakeBuild),
				"SHA256SUMS": sumsLine([]byte(fakeBuild), tc.asset),
			}}, "--origin", testOrigin)
			if r.code != 0 || r.buildArgs == nil {
				t.Fatalf("exit code %d, build ran with %q, stderr: %s", r.code, r.buildArgs, r.stderr)
			}
			if !slices.Contains(r.urls, downloadBase+tc.asset) {
				t.Errorf("downloaded %q, want %s among them", r.urls, tc.asset)
			}
		})
	}
}

func TestInstallShSaysWhenThereIsNoBuildForThisComputer(t *testing.T) {
	for _, tc := range []struct{ os, arch string }{{"Linux", "aarch64"}, {"FreeBSD", "amd64"}} {
		t.Run(tc.os+"-"+tc.arch, func(t *testing.T) {
			r := runInstallSh(t, computer{os: tc.os, arch: tc.arch})
			if r.code != 1 || r.urls != nil {
				t.Errorf("exit code %d, downloaded %q; want 1 and nothing", r.code, r.urls)
			}
			want := "error: printpipe has no build for " + tc.os + " " + tc.arch + "\n"
			if !strings.HasPrefix(r.stderr, want) {
				t.Errorf("stderr %q, want it to start with %q", r.stderr, want)
			}
		})
	}
}

func TestInstallShChecksWithShasumOnAMac(t *testing.T) {
	r := runInstallSh(t, computer{noSHA256: true}, "--origin", testOrigin)
	if r.code != 0 || r.buildArgs == nil {
		t.Errorf("exit code %d, build ran with %q, stderr: %s", r.code, r.buildArgs, r.stderr)
	}

	wantRefused(t, runInstallSh(t, computer{noSHA256: true, release: tamperedRelease()}))
}

func TestInstallShSaysWhenItCouldNotReachGitHub(t *testing.T) {
	r := runInstallSh(t, computer{curlExit: 6}, "--origin", testOrigin)

	wantRefused(t, r)
	want := "error: could not reach GitHub\n  Check the internet connection and run the same command again.\n"
	if r.stderr != want {
		t.Errorf("stderr %q, want %q", r.stderr, want)
	}
	wantNoLeftovers(t, r)
}

func TestInstallShSaysWhichFileGitHubDidNotGive(t *testing.T) {
	release := linuxRelease()
	delete(release, "SHA256SUMS")

	r := runInstallSh(t, computer{release: release}, "--origin", testOrigin)

	wantRefused(t, r)
	if want := "error: could not download SHA256SUMS from GitHub\n"; !strings.HasPrefix(r.stderr, want) {
		t.Errorf("stderr %q, want it to start with %q", r.stderr, want)
	}
}

func TestInstallShEndsWithTheExitCodeOfInstallAndDeletesTheBuild(t *testing.T) {
	r := runInstallSh(t, computer{buildExit: 1}, "--origin", testOrigin)

	if r.code != 1 || r.buildArgs == nil {
		t.Errorf("exit code %d, build ran with %q; want 1 and run", r.code, r.buildArgs)
	}
	wantNoLeftovers(t, r)
}

func TestInstallShCutShortDoesNothing(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	r := runInstallSh(t, computer{script: script[:len(script)-len("main \"$@\"\n")]})
	if r.urls != nil || r.buildArgs != nil {
		t.Errorf("without its last line it downloaded %q and ran %q", r.urls, r.buildArgs)
	}
	r = runInstallSh(t, computer{script: script[:len(script)/2]})
	if r.urls != nil || r.buildArgs != nil {
		t.Errorf("cut in half it downloaded %q and ran %q", r.urls, r.buildArgs)
	}
}

func TestInstallShDoesNotBlameTheNetworkForWhatIsNot(t *testing.T) {
	r := runInstallSh(t, computer{curlExit: 23}, "--origin", testOrigin) // curl could not write

	wantRefused(t, r)
	if want := "error: could not download " + linuxBuild + " from GitHub\n"; !strings.HasPrefix(r.stderr, want) {
		t.Errorf("stderr %q, want it to start with %q", r.stderr, want)
	}
}

func TestInstallShSaysWhenItHasNoTempFolder(t *testing.T) {
	r := runInstallSh(t, computer{noTemp: true}, "--origin", testOrigin)

	wantRefused(t, r)
	want := "error: could not make a temporary folder\n  Check that the temp folder is writable and not full, then run the same command again.\n"
	if r.stderr != want {
		t.Errorf("stderr %q, want %q", r.stderr, want)
	}
}

// The shell's 126 is a file it may not run, as in a temp folder mounted noexec.
func TestInstallShSaysWhenTheTempFolderDoesNotLetTheBuildRun(t *testing.T) {
	r := runInstallSh(t, computer{buildExit: 126}, "--origin", testOrigin)

	if r.code != 1 {
		t.Errorf("exit code %d, want 1", r.code)
	}
	if want := "error: could not run the download in the temp folder\n"; !strings.HasPrefix(r.stderr, want) {
		t.Errorf("stderr %q, want it to start with %q", r.stderr, want)
	}
	wantNoLeftovers(t, r)
}
