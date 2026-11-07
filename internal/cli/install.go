package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/layout"
	"github.com/alexgred/printpipe/internal/models"
	"github.com/alexgred/printpipe/internal/printer"
)

// install takes a computer to a running agent and a printed test label, and can
// be run again at any time. A failing step leaves what the steps before it did,
// so that the same command picks up from there.
func install(e env) error {
	cfg, path, err := loadConfig(e, "", false)
	if err != nil {
		return err
	}
	next, chosen, err := planInstall(e, cfg)
	if err != nil {
		return err
	}
	size, err := testLabelSize(e, next)
	if err != nil {
		return err
	}
	svc, err := serviceOf(e, "install")
	if err != nil {
		return err
	}
	files, err := filesOf(e.sys)
	if err != nil {
		return err
	}

	if err := placeBinary(e, files); err != nil {
		return err
	}
	e.program = files.Binary

	// The agent is found by the port in the file, the one it listens on. Another
	// program there is no agent to restart, and matters only if it stays the port.
	running, portErr := findAgent(e, cfg.Port)
	var foreign *failure
	if portErr != nil && !errors.As(portErr, &foreign) {
		return portErr
	}
	// What is written when install stops here: the origin stays, so that the fix
	// is to run the same command. A running agent keeps its port in the file, or
	// no command could find it.
	stopWith := func(cause error) error {
		kept := next
		if running != nil {
			kept.Port = cfg.Port
		}
		if err := saveConfig(e, path, kept); err != nil {
			return err
		}
		return cause
	}

	if next, err = findPrinter(e, next, path, chosen); err != nil {
		return stopWith(err)
	}
	if err := e.say("%s", printerLine(next)); err != nil {
		return err
	}
	if next.Port != cfg.Port {
		if err := checkPortFree(e, next.Port); err != nil {
			return stopWith(err)
		}
	} else if portErr != nil {
		return stopWith(portErr)
	}

	if err := saveConfig(e, path, next); err != nil {
		return err
	}
	if err := e.say("Config: %s", path); err != nil {
		return err
	}
	if err := svc.refreshStartup(e); err != nil {
		return stopWith(err)
	}

	if running == nil {
		err = startAgent(e, svc, next)
	} else if err = svc.restart(e, cfg.Port, running.PID); err != nil {
		return stopWith(err)
	} else {
		err = sayRestarted(e, next.Port)
	}
	if err != nil {
		return err
	}
	// Nothing runs from it now. It may still be held if the old agent is slow to
	// go, and the next install tries again.
	_ = removeIfThere(e.sys, files.OldBinary)

	if _, skip := e.flags["no-test-print"]; skip {
		err = e.say("Test label: skipped")
	} else {
		err = sendTestLabel(e, next, size)
	}
	if err != nil {
		return err
	}
	if svc.startsAtLogin(e) {
		return e.say("Starts at login: on")
	}
	return e.say("To start the agent at login: printpipe startup on")
}

// planInstall is the configuration with the command's flags applied, once they
// all are shown to be able to work, and on macOS and Linux the printer that
// --device usb:… chooses to make a queue for: it cannot be printed to as a device
// there, so it is never written to the configuration.
func planInstall(e env, cfg config.Config) (config.Config, *printer.USBID, error) {
	var chosen *printer.USBID
	device, hasDevice := e.flags["device"]
	if _, hasQueue := e.flags["queue"]; hasDevice && !hasQueue && e.sys.GOOS() != "windows" && strings.HasPrefix(device, "usb:") {
		id, err := printer.ParseUSB(device)
		if err != nil {
			return cfg, nil, usageError(err.Error())
		}
		chosen = &id
		// applyFlags would refuse it, and it is not a setting.
		delete(e.flags, "device")
	}
	next, err := applyFlags(e, cfg)
	if err != nil {
		return cfg, nil, err
	}
	if len(next.Origins) == 0 {
		return cfg, nil, usageError("--origin is needed the first time: the page allowed to print, such as https://app.example")
	}
	return next, chosen, nil
}

// placeBinary puts this program where it is installed and says so, and what it
// takes to run it by name.
func placeBinary(e env, files layout.Paths) error {
	exe, err := e.sys.Executable()
	if err != nil {
		return err
	}
	if !samePath(e.sys.GOOS(), exe, files.Binary) {
		self, err := e.sys.ReadFile(exe)
		if err != nil {
			return err
		}
		// The same program, also through a link to it, needs no swap: run again, it
		// would find the old agent's file still in use where the swap moves it to.
		if current, err := e.sys.ReadFile(files.Binary); err != nil || !bytes.Equal(current, self) {
			if err := swapBinary(e.sys, files, self); err != nil {
				return &failure{
					msg:   fmt.Sprintf("could not place printpipe in %s: %v", files.BinaryDir, err),
					hints: []string{"If the agent is running, stop it and run the same command again: printpipe stop"},
				}
			}
		}
	}
	if err := clearDownloadMark(e, files.Binary); err != nil {
		return err
	}
	if err := e.say("Installed printpipe %s: %s", Version, files.Binary); err != nil {
		return err
	}
	return sayPath(e, files.BinaryDir)
}

// swapBinary replaces the binary by renames alone, never opening it for writing:
// it may be running, and a rename is what works on one on every OS.
func swapBinary(sys System, files layout.Paths, self []byte) error {
	if err := sys.WriteProgram(files.NewBinary, self); err != nil {
		return err
	}
	if err := swapIn(sys, files); err != nil {
		_ = sys.Remove(files.NewBinary)
		return err
	}
	return nil
}

func swapIn(sys System, files layout.Paths) error {
	if err := removeIfThere(sys, files.OldBinary); err != nil {
		return err
	}
	err := sys.Rename(files.Binary, files.OldBinary)
	hadBinary := !errors.Is(err, fs.ErrNotExist)
	if err != nil && hadBinary {
		return err
	}
	if err := sys.Rename(files.NewBinary, files.Binary); err != nil {
		if hadBinary {
			_ = sys.Rename(files.OldBinary, files.Binary)
		}
		return err
	}
	return nil
}

// clearDownloadMark takes off the mark a browser leaves on a download, for which
// the OS warns at logon: the Zone.Identifier stream on NTFS, which a rename keeps,
// and macOS's quarantine attribute, as a safeguard, which having none fails on.
func clearDownloadMark(e env, binary string) error {
	switch e.sys.GOOS() {
	case "windows":
		if err := removeIfThere(e.sys, binary+":Zone.Identifier"); err != nil {
			return &failure{
				msg: fmt.Sprintf("could not take the download mark off %s: %v", binary, err),
				hints: []string{
					"Windows will warn about it at logon. In PowerShell: Unblock-File '" + binary + "'",
					"Then run the same command again.",
				},
			}
		}
	case "darwin":
		_, _ = e.sys.Run(e.ctx, nil, "xattr", "-d", "com.apple.quarantine", binary)
	}
	return nil
}

// sayPath sees to the command working by name: Windows has the folder added to the
// user's Path, while macOS and Linux get the line to add, their profiles being the
// user's own.
func sayPath(e env, folder string) error {
	if e.sys.GOOS() != "windows" {
		onPath := slices.ContainsFunc(strings.Split(e.sys.PathEnv(), ":"), func(entry string) bool {
			return strings.TrimRight(entry, "/") == folder
		})
		if onPath {
			return nil
		}
		return e.say(`~/.local/bin is not on PATH; add this line to your shell profile: export PATH="$HOME/.local/bin:$PATH"`)
	}
	current, err := e.sys.UserPath()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(strings.Split(current, ";"), isPathEntry(folder)) {
		return nil
	}
	if trimmed := strings.TrimRight(current, ";"); trimmed != "" {
		folder = trimmed + ";" + folder
	}
	if err := e.sys.SetUserPath(folder); err != nil {
		return err
	}
	return e.say("Added to your Path; a new terminal will find printpipe by name")
}

// isPathEntry is whether an entry of the user's Path on Windows names the folder.
func isPathEntry(folder string) func(string) bool {
	return func(entry string) bool {
		return strings.EqualFold(strings.TrimRight(entry, `\`), folder)
	}
}

// findPrinter is the configuration with the printer found: what was there on a
// failure, for the caller to keep.
func findPrinter(e env, cfg config.Config, path string, chosen *printer.USBID) (config.Config, error) {
	if e.sys.GOOS() == "windows" {
		return windowsPrinter(e, cfg)
	}
	return cupsPrinter(e, cfg, path, chosen)
}

// windowsPrinter is the configuration with the printer to print to on Windows: a
// queue that is there, which is used only when named, a saved device that is
// still connected, else the known model that is.
func windowsPrinter(e env, cfg config.Config) (config.Config, error) {
	if cfg.Queue != "" {
		return cfg, checkUserPrinter(e, cfg)
	}
	usb, err := printer.ListUSB(e.ctx, e.sys)
	if err != nil {
		return cfg, fmt.Errorf("could not list the USB printers: %w", err)
	}
	if cfg.Device != "" {
		id, err := printer.ParseUSB(cfg.Device)
		if err != nil {
			return cfg, err
		}
		var match []printer.USBPrinter
		for _, p := range usb {
			if id.Matches(p.USBID) {
				match = append(match, p)
			}
		}
		switch _, named := e.flags["device"]; {
		case len(match) == 1:
			return cfg, nil
		case len(match) > 1:
			// The matches share their IDs, so all are of a known model or none.
			if known := knownAmong(match); len(known) > 0 {
				return cfg, severalConnected("windows", known)
			}
			return cfg, &failure{msg: fmt.Sprintf("%d printers match %s", len(match), id), hints: chooseAmong(configDevice, match)}
		case named:
			return cfg, notConnected(id)
		}
	}
	known := knownAmong(usb)
	switch len(known) {
	case 0:
		return cfg, noKnownPrinter(usb)
	case 1:
		cfg.Device = printer.USBID{VID: known[0].id.VID, PID: known[0].id.PID}.String()
		return cfg, nil
	default:
		return cfg, severalConnected("windows", known)
	}
}

// checkUserPrinter is nil when the configured device file or Windows queue is
// there. Only a user ever sets one, so install never replaces one that is gone
// with a printer it finds.
func checkUserPrinter(e env, cfg config.Config) error {
	what, another := "device file "+cfg.Device, "<file>"
	if cfg.Queue != "" {
		what = "printer queue " + cfg.Queue
	}
	if e.sys.GOOS() == "windows" {
		another = "usb:VID:PID"
	}
	there, err := printerExists(e, cfg)
	if err != nil {
		return &failure{msg: fmt.Sprintf("could not check the %s: %v", what, err)}
	}
	if there {
		return nil
	}
	return &failure{
		msg: fmt.Sprintf("the %s is not there", what),
		hints: []string{
			"Plug the printer in, switch it on and run the same command again.",
			"Another printer: printpipe config --device " + another + ", or --queue <name>",
		},
	}
}

// noKnownPrinter lists what is connected, for a model printpipe does not know.
func noKnownPrinter(usb []printer.USBPrinter) error {
	found := "No USB printers found."
	if len(usb) > 0 {
		names := make([]string, len(usb))
		for i, p := range usb {
			names[i] = p.String()
		}
		found = "USB printers found: " + strings.Join(names, ", ")
	}
	return &failure{
		msg: "no known label printer is connected",
		hints: []string{
			found,
			"Plug the " + models.Default().Name + " in, switch it on and run the same command again.",
			"Another model: printpipe config --device usb:VID:PID",
		},
	}
}

// printerLine says what will be printed to.
func printerLine(cfg config.Config) string {
	if cfg.Queue != "" {
		return "Printer: queue " + cfg.Queue
	}
	if m, ok := models.ForDevice(cfg.Device); ok {
		return fmt.Sprintf("Printer: %s (%s)", m.Name, cfg.Device)
	}
	return "Printer: device " + cfg.Device
}
