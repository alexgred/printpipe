package cli

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/printer"
)

// configure is the config command: without flags it shows the configuration,
// with flags it changes it.
func configure(e env) error {
	cfg, path, err := loadConfig(e, "", false)
	if err != nil {
		return err
	}
	if len(e.flags) == 0 {
		return e.say("Port: %d\nOrigins: %s\nQueue: %s\nDevice: %s", cfg.Port, shown(strings.Join(cfg.Origins, ", ")), shown(cfg.Queue), shown(cfg.Device))
	}

	next, err := applyFlags(e, cfg)
	if err != nil {
		return err
	}
	for _, origin := range e.values["remove-origin"] {
		if !slices.Contains(cfg.Origins, origin) {
			if err := e.say("Not in origins: %s", origin); err != nil {
				return err
			}
		}
	}
	lines := changes(cfg, next)
	if len(lines) == 0 {
		return e.say("Nothing changed.")
	}
	if next.Port != cfg.Port {
		lines = append(lines, "Warning: a client's agent URL must use the same port: "+agentURL(next.Port))
	}

	// Another program on the port is no agent to restart: moving off that port
	// may be what the change is for.
	agent, err := findAgent(e, cfg.Port)
	var foreign *failure
	portTaken := errors.As(err, &foreign)
	if err != nil && !portTaken {
		return err
	}
	if agent == nil {
		if err := saveConfig(e, path, next); err != nil {
			return err
		}
		if err := e.say("%s", strings.Join(lines, "\n")); err != nil {
			return err
		}
		if portTaken && next.Port == cfg.Port {
			return e.say("The agent is not running, and port %d is used by another program: it cannot start until that is free.", cfg.Port)
		}
		return e.say("The agent is not running: the change applies when it starts.")
	}

	svc, err := serviceOf(e, "config")
	if err != nil {
		return err
	}
	if next.Port != cfg.Port {
		if err := checkPortFree(e, next.Port); err != nil {
			return err
		}
	}
	if err := saveConfig(e, path, next); err != nil {
		return err
	}
	if err := svc.restart(e, cfg.Port, agent.PID); err != nil {
		return keepOldConfig(e, path, cfg, err)
	}
	if err := e.say("%s", strings.Join(lines, "\n")); err != nil {
		return err
	}
	return sayRestarted(e, next.Port)
}

// checkPortFree fails when anything answers on the port a running agent would
// move to, so that the agent is not taken down for a port it could not get.
func checkPortFree(e env, port int) error {
	other, err := findAgent(e, port)
	if err != nil {
		return err
	}
	if other != nil {
		return portBusy(port)
	}
	return nil
}

// keepOldConfig puts config.json back after a restart that failed: an agent
// left on the old port, with the new one in the file, no command could find.
func keepOldConfig(e env, path string, cfg config.Config, restartErr error) error {
	if err := saveConfig(e, path, cfg); err != nil {
		return fmt.Errorf("%v; config.json keeps the change: %v", restartErr, err)
	}
	if err := e.say("config.json was left as it was."); err != nil {
		return err
	}
	return restartErr
}

// applyFlags is the configuration with the command's flags applied, once they
// all are shown to be able to work.
func applyFlags(e env, cfg config.Config) (config.Config, error) {
	queue, hasQueue := e.flags["queue"]
	device, hasDevice := e.flags["device"]
	if hasQueue && hasDevice {
		return cfg, usageError("use either --queue or --device, not both")
	}
	// Each replaces the other: the agent refuses a config with both.
	if hasQueue {
		if err := checkQueue(e.sys.GOOS(), queue); err != nil {
			return cfg, err
		}
		cfg.Queue, cfg.Device = queue, ""
	}
	if hasDevice {
		device, err := checkedDevice(e.sys.GOOS(), device)
		if err != nil {
			return cfg, err
		}
		cfg.Queue, cfg.Device = "", device
	}
	if value, ok := e.flags["port"]; ok {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return cfg, usageError(fmt.Sprintf("--port %q is not a port: want 1 to 65535", value))
		}
		cfg.Port = port
	}

	added, removed := e.values["origin"], e.values["remove-origin"]
	for _, origin := range added {
		if err := checkOrigin(origin); err != nil {
			return cfg, err
		}
		if slices.Contains(removed, origin) {
			return cfg, usageError(fmt.Sprintf("--origin and --remove-origin both name %s", origin))
		}
	}
	before := cfg.Origins
	cfg.Origins = slices.DeleteFunc(slices.Clone(cfg.Origins), func(origin string) bool { return slices.Contains(removed, origin) })
	for _, origin := range added {
		if !slices.Contains(cfg.Origins, origin) {
			cfg.Origins = append(cfg.Origins, origin)
		}
	}
	if len(before) > 0 && len(cfg.Origins) == 0 {
		return cfg, &failure{
			msg:   "cannot remove the last origin: no page could print",
			hints: []string{"Replace it: printpipe config --origin <url> --remove-origin " + before[0]},
		}
	}
	return cfg, nil
}

// checkQueue refuses a name no queue can have: CUPS would only say later, on
// the first job, that there is no such queue. A Windows printer's name may
// have spaces.
func checkQueue(goos, queue string) error {
	switch {
	case queue == "":
		return usageError("--queue needs a name")
	case goos == "windows":
		return nil
	case strings.HasPrefix(queue, "-"):
		return usageError(fmt.Sprintf("--queue %q: a queue's name does not start with -", queue))
	case strings.ContainsAny(queue, " \t/#"):
		return usageError(fmt.Sprintf("--queue %q: a CUPS queue's name has no space, tab, / or #", queue))
	}
	return nil
}

// checkedDevice is the device as config.json has it, on an OS that can print
// to it: a usb: device on Windows, a device file on Linux.
func checkedDevice(goos, device string) (string, error) {
	if device == "" {
		return "", usageError("--device needs a value")
	}
	switch goos {
	case "windows":
		id, err := printer.ParseUSB(device)
		if err != nil {
			return "", usageError(err.Error())
		}
		return id.String(), nil
	case "linux":
		if !strings.HasPrefix(device, "/") {
			return "", usageError(fmt.Sprintf("--device %q: on Linux use a device file such as /dev/usb/lp0, or --queue", device))
		}
		return device, nil
	default:
		return "", usageError(fmt.Sprintf("--device %q: on macOS use --queue", device))
	}
}

// checkOrigin refuses what no browser sends as Origin: the agent compares the
// two as they are, so such an origin would never be allowed to print.
func checkOrigin(origin string) error {
	if !isBrowserOrigin(origin) {
		return usageError(fmt.Sprintf("--origin %q is not an origin: want the scheme and host a browser sends, in small letters, such as https://app.example or http://localhost:3000", origin))
	}
	return nil
}

// isBrowserOrigin is whether a browser could send this Origin: only the scheme
// and host, in small letters and ASCII (a name in another script as punycode),
// with no port when it is the scheme's default.
func isBrowserOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return false
	}
	if origin != u.Scheme+"://"+u.Host || origin != strings.ToLower(origin) || strings.HasSuffix(origin, ":") {
		return false
	}
	if strings.ContainsFunc(origin, func(r rune) bool { return r > unicode.MaxASCII }) {
		return false
	}
	if u.Port() == "" {
		return true
	}
	port, err := strconv.Atoi(u.Port())
	defaultPort := (u.Scheme == "http" && port == 80) || (u.Scheme == "https" && port == 443)
	return err == nil && port >= 1 && port <= 65535 && !defaultPort
}

// changes says what differs between two configurations, line by line.
func changes(from, to config.Config) []string {
	var lines []string
	if from.Port != to.Port {
		lines = append(lines, fmt.Sprintf("Port: %d -> %d", from.Port, to.Port))
	}
	for _, origin := range to.Origins {
		if !slices.Contains(from.Origins, origin) {
			lines = append(lines, "Origins: added "+origin)
		}
	}
	for _, origin := range from.Origins {
		if !slices.Contains(to.Origins, origin) {
			lines = append(lines, "Origins: removed "+origin)
		}
	}
	if from.Queue != to.Queue {
		lines = append(lines, fmt.Sprintf("Queue: %s -> %s", shown(from.Queue), shown(to.Queue)))
	}
	if from.Device != to.Device {
		lines = append(lines, fmt.Sprintf("Device: %s -> %s", shown(from.Device), shown(to.Device)))
	}
	return lines
}

// shown is a setting as the user reads it.
func shown(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

// saveConfig replaces config.json in one step, so that a write cut short does
// not leave a file that no command can read.
func saveConfig(e env, path string, cfg config.Config) error {
	b, err := cfg.Encode()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := e.sys.WriteFile(tmp, b); err != nil {
		return err
	}
	if err := e.sys.Rename(tmp, path); err != nil {
		_ = e.sys.Remove(tmp)
		return err
	}
	return nil
}
