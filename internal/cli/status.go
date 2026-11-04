package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"slices"
	"strings"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/layout"
	"github.com/alexgred/printpipe/internal/printer"
)

// status answers whether printing works here and where everything is. It only
// asks this computer, and never the printer, of which it says only whether the
// OS has it.
func status(e env) error {
	svc, err := serviceOf(e, "status")
	if err != nil {
		return err
	}
	cfg, _, err := loadConfig(e, "", false)
	if err != nil {
		return err
	}
	files, err := filesOf(e.sys)
	if err != nil {
		return err
	}
	// Another program or another user's agent on the port is no agent of this
	// user's, and the rest is still worth seeing.
	agent, agentErr := findAgent(e, cfg.Port)
	var foreign *failure
	if agentErr != nil && !errors.As(agentErr, &foreign) {
		return agentErr
	}
	if agent != nil {
		if _, err := agentExe(e, cfg.Port, agent.PID); err != nil {
			agent, agentErr = nil, err
		}
	}
	lines := []string{
		agentLine(e, svc, agent),
		versionLine(e, files, agent),
		fmt.Sprintf("Port: %d", cfg.Port),
		"Starts at login: " + onOff(svc.startsAtLogin(e)),
		printerStatusLine(e, cfg),
		"Binary: " + files.Binary,
		"Config: " + files.Config,
		"Log: " + files.Log,
	}
	if err := e.say("%s", strings.Join(lines, "\n")); err != nil {
		return err
	}
	if agentErr != nil {
		return agentErr
	}
	if agent == nil {
		return agentNotRunning()
	}
	return nil
}

// agentLine says whether the agent runs and what keeps it running.
func agentLine(e env, svc service, agent *runningAgent) string {
	if agent == nil {
		return "Agent: not running"
	}
	if manager := svc.manager(e, agent.PID); manager != "" {
		return fmt.Sprintf("Agent: running under %s, pid %d", manager, agent.PID)
	}
	return fmt.Sprintf("Agent: running with no service manager, pid %d", agent.PID)
}

// versionLine says which version is installed and which runs, and what to do
// when they differ.
func versionLine(e env, files layout.Paths, agent *runningAgent) string {
	installed, known := installedVersion(e, files)
	if agent == nil {
		return "Version: installed " + installed
	}
	line := fmt.Sprintf("Version: installed %s, running %s", installed, agent.Version)
	if known && agent.Version != installed {
		line += "\n  The agent runs another version than is installed: printpipe restart"
	}
	return line
}

// installedVersion is the version of the installed binary, as shown, and whether
// there is one to tell: this printpipe may be a copy of another version.
func installedVersion(e env, files layout.Paths) (string, bool) {
	if exe, err := e.sys.Executable(); err == nil && samePath(e.sys.GOOS(), exe, files.Binary) {
		return Version, true
	}
	if _, err := e.sys.ReadFile(files.Binary); errors.Is(err, fs.ErrNotExist) {
		return "(none)", false
	}
	out, err := e.sys.Run(e.ctx, nil, files.Binary, "version")
	if err != nil {
		return "(unknown)", false
	}
	version, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "printpipe ")
	if !ok {
		return "(unknown)", false
	}
	return version, true
}

// printerStatusLine says what is configured to be printed to and whether the OS
// has it.
func printerStatusLine(e env, cfg config.Config) string {
	if cfg.Queue == "" && cfg.Device == "" {
		return "Printer: (none)"
	}
	exists, err := printerExists(e, cfg)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return printerLine(cfg) + ", could not check: CUPS is not installed"
	case err != nil:
		return printerLine(cfg) + ", could not check: " + err.Error()
	case exists:
		return printerLine(cfg) + ", exists"
	default:
		return printerLine(cfg) + ", not found"
	}
}

// printerExists is whether the OS has the configured queue or device.
func printerExists(e env, cfg config.Config) (bool, error) {
	if cfg.Queue != "" {
		return printer.QueueExists(e.ctx, e.sys, cfg.Queue)
	}
	if e.sys.GOOS() == "windows" {
		return usbDeviceConnected(e, cfg.Device)
	}
	return deviceFileExists(e.sys, cfg.Device)
}

// usbDeviceConnected is whether a printer the usb: device names is plugged in:
// until it is, Windows has no such device.
func usbDeviceConnected(e env, device string) (bool, error) {
	id, err := printer.ParseUSB(device)
	if err != nil {
		return false, err
	}
	usb, err := printer.ListUSB(e.ctx, e.sys)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(usb, func(p printer.USBPrinter) bool { return id.Matches(p.USBID) }), nil
}

// deviceFileExists looks for a device file in its folder: opening it, even to
// read, could disturb the printer.
func deviceFileExists(sys System, device string) (bool, error) {
	names, err := sys.ReadDir(path.Dir(device))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.Contains(names, path.Base(device)), nil
}
