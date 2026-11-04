package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	"github.com/alexgred/printpipe/internal/layout"
)

// releasesURL is where printpipe's releases are. /latest redirects to the latest
// one's tag, with no REST API and no rate limit.
const releasesURL = "https://github.com/alexgred/printpipe/releases"

// checksumsAsset is the release's list of hashes of its builds.
const checksumsAsset = "SHA256SUMS"

// upgrade installs the latest release when it is newer than this printpipe.
func upgrade(e env) error {
	files, err := filesOf(e.sys)
	if err != nil {
		return err
	}
	if err := checkInstalledCopy(e, files); err != nil {
		return err
	}
	latest, err := latestTag(e)
	if err != nil {
		return err
	}
	if own, ok := parseSemver(Version); ok {
		newest, _ := parseSemver(latest)
		switch c := own.compare(newest); {
		case c == 0:
			return e.say("printpipe %s is the latest version", Version)
		case c > 0:
			return e.say("installed %s is newer than the latest release %s; nothing to do", Version, latest)
		}
	}
	svc, err := serviceOf(e, "upgrade")
	if err != nil {
		return err
	}
	port, agent, err := findOwnAgent(e)
	if err != nil {
		return err
	}
	if err := e.say("Downloading printpipe %s", latest); err != nil {
		return err
	}
	if err := prepareNewBinary(e, files, latest); err != nil {
		_ = removeIfThere(e.sys, files.NewBinary)
		return err
	}
	if err := swapIn(e.sys, files); err != nil {
		_ = removeIfThere(e.sys, files.NewBinary)
		return &failure{msg: fmt.Sprintf("could not replace %s: %v", files.Binary, err)}
	}
	if agent == nil {
		_ = removeIfThere(e.sys, files.OldBinary)
		return e.say("updated to %s; the agent is not running, it will use the new version when started", latest)
	}
	e.program = files.Binary
	if err := restartAs(e, svc, port, agent.PID, latest); err != nil {
		return rollBack(e, svc, port, files, fmt.Sprintf("%s did not come up: %v", latest, err))
	}
	// Windows holds this file while the command that runs from it lasts, and the
	// next upgrade tries again.
	_ = removeIfThere(e.sys, files.OldBinary)
	return e.say("updated to %s; agent restarted", latest)
}

// findOwnAgent is the configured port and this user's agent on it, nil for none.
// Another program there is no agent, and is left alone. An agent that could not be
// restarted fails here, while nothing has changed.
func findOwnAgent(e env) (int, *runningAgent, error) {
	cfg, _, err := loadConfig(e, "", false)
	if err != nil {
		return 0, nil, err
	}
	agent, err := findAgent(e, cfg.Port)
	var foreign *failure
	if err != nil && !errors.As(err, &foreign) {
		return 0, nil, err
	}
	if agent != nil {
		if err := checkOwnAgent(e, cfg.Port, agent.PID); err != nil {
			return 0, nil, err
		}
	}
	return cfg.Port, agent, nil
}

// restartAs replaces the running agent and waits for it to answer as this version:
// the old one may answer for a moment yet.
func restartAs(e env, svc service, port, pid int, version string) error {
	if err := svc.restart(e, port, pid); err != nil {
		return err
	}
	return waitForVersion(e, port, version)
}

// waitForVersion waits through anything else the port answers: an agent on its way
// down may answer oddly for a moment, and that is no reason to give up on the new one.
func waitForVersion(e env, port int, version string) error {
	deadline := e.sys.Now().Add(agentWaitSeconds * time.Second)
	for {
		agent, err := findAgent(e, port)
		if e.ctx.Err() != nil {
			return e.ctx.Err()
		}
		if err == nil && agent != nil && agent.Version == version {
			return nil
		}
		if !e.sys.Now().Before(deadline) {
			return fmt.Errorf("it did not answer as %s within %d s", version, agentWaitSeconds)
		}
		e.sys.Sleep(100 * time.Millisecond)
	}
}

// rollBack puts the old binary back and runs it. It goes on through whatever
// fails, and through Ctrl+C: what counts is the working binary being back.
func rollBack(e env, svc service, port int, files layout.Paths, cause string) error {
	e.ctx = context.WithoutCancel(e.ctx)
	stopForRollback(e, svc, port)
	if err := restoreBinary(e.sys, files); err != nil {
		return &failure{
			msg:   fmt.Sprintf("could not roll back to %s: %v", Version, err),
			hints: []string{cause, "Log: " + files.Log},
		}
	}
	rolledBack := &failure{msg: fmt.Sprintf("rolled back to %s, log: %s", Version, files.Log), hints: []string{cause}}
	err := svc.start(e)
	if err == nil {
		err = waitForVersion(e, port, Version)
	}
	if err != nil {
		rolledBack.hints = append(rolledBack.hints, "The agent did not come back: printpipe start")
	}
	return rolledBack
}

// stopForRollback stops the agent that answers. Without one, the new build may be
// running all the same, hung or still coming up, and only a manager can stop that.
// When stopping fails, starting the old one is what shows it.
func stopForRollback(e env, svc service, port int) {
	agent, err := findAgent(e, port)
	if err != nil || agent == nil {
		_ = svc.stopStarting(e)
		return
	}
	if svc.stop(e, port, agent.PID) == nil {
		_ = waitForAgentGone(e, port)
	}
}

// restoreBinary swaps back by renames alone, as the swap did: nothing overwrites
// a file that may still be in use. The new build goes where it came from, and is
// put back when the old one will not move, so that a program stays in place.
func restoreBinary(sys System, files layout.Paths) error {
	if err := sys.Rename(files.Binary, files.NewBinary); err != nil {
		return err
	}
	if err := sys.Rename(files.OldBinary, files.Binary); err != nil {
		_ = sys.Rename(files.NewBinary, files.Binary)
		return err
	}
	_ = sys.Remove(files.NewBinary)
	return nil
}

// checkInstalledCopy refuses any copy but the installed one: upgrading another
// would leave two.
func checkInstalledCopy(e env, files layout.Paths) error {
	exe, err := e.sys.Executable()
	if err != nil {
		return err
	}
	if samePath(e.sys.GOOS(), exe, files.Binary) {
		return nil
	}
	if _, err := e.sys.ReadFile(files.Binary); errors.Is(err, fs.ErrNotExist) {
		return &failure{msg: "printpipe is not installed", hints: []string{"Install it: printpipe install --origin <url>"}}
	}
	return &failure{
		msg:   "this copy of printpipe is not the installed one, " + files.Binary,
		hints: []string{"Upgrade the installed one: " + files.Binary + " upgrade"},
	}
}

// prepareNewBinary puts the build of this tag beside the binary, ready to be
// swapped in: it matches the published checksum, and it runs and is that version.
func prepareNewBinary(e env, files layout.Paths, tag string) error {
	asset := "printpipe-" + e.sys.GOOS() + "-" + e.sys.GOARCH()
	if e.sys.GOOS() == "windows" {
		asset += ".exe"
	}
	binary, err := downloadAsset(e, tag, asset)
	if err != nil {
		return err
	}
	sums, err := downloadAsset(e, tag, checksumsAsset)
	if err != nil {
		return err
	}
	if err := verifyChecksum(binary, sums, asset); err != nil {
		return err
	}
	if err := e.sys.WriteProgram(files.NewBinary, binary); err != nil {
		return err
	}
	if err := clearDownloadMark(e, files.NewBinary); err != nil {
		return err
	}
	out, err := e.sys.Run(e.ctx, nil, files.NewBinary, "version")
	if err != nil {
		return &failure{msg: fmt.Sprintf("the downloaded printpipe does not run: %v", err)}
	}
	if said := strings.TrimSpace(string(out)); said != "printpipe "+tag {
		return &failure{msg: fmt.Sprintf("the downloaded printpipe says it is %q, not %s", said, tag)}
	}
	return nil
}

func downloadAsset(e env, tag, asset string) ([]byte, error) {
	body, err := e.sys.Download(e.ctx, releasesURL+"/download/"+tag+"/"+asset)
	if err != nil {
		return nil, webFailure(e, fmt.Sprintf("could not download %s of %s", asset, tag), err)
	}
	return body, nil
}

// verifyChecksum checks a build against its line in SHA256SUMS: a hash, spaces,
// and the name, with a * before it when made in binary mode.
func verifyChecksum(binary, sums []byte, asset string) error {
	nothingInstalled := []string{"Nothing was installed."}
	for line := range strings.Lines(string(sums)) {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		sum := sha256.Sum256(binary)
		if !strings.EqualFold(fields[0], hex.EncodeToString(sum[:])) {
			return &failure{msg: "the download does not match " + checksumsAsset + ": it is damaged or was tampered with", hints: nothingInstalled}
		}
		return nil
	}
	return &failure{msg: checksumsAsset + " does not list " + asset, hints: nothingInstalled}
}

// latestTag is the tag /releases/latest redirects to. With no release at all
// GitHub redirects to the list of releases, which names no tag.
func latestTag(e env) (string, error) {
	location, err := e.sys.Redirect(e.ctx, releasesURL+"/latest")
	if err != nil {
		return "", webFailure(e, "could not find the latest release on GitHub", err)
	}
	if parsed, err := url.Parse(location); err == nil {
		if _, tag, ok := strings.Cut(parsed.Path, "/releases/tag/"); ok {
			if _, ok := parseSemver(tag); ok {
				return tag, nil
			}
		}
	}
	return "", &failure{msg: "could not find the latest release on GitHub: it answered with " + location}
}

// webFailure says why the web did not give what was asked for: GitHub's answer when
// there was one, what is wrong with the connection when there was not.
func webFailure(e env, what string, err error) error {
	if e.ctx.Err() != nil {
		return e.ctx.Err()
	}
	var status httpStatus
	if errors.As(err, &status) {
		return &failure{msg: fmt.Sprintf("%s: %v", what, status)}
	}
	return &failure{
		msg:   "could not reach GitHub",
		hints: []string{"Check the internet connection and run printpipe upgrade again."},
	}
}
