package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/models"
	"github.com/alexgred/printpipe/internal/printer"
	"github.com/alexgred/printpipe/internal/testlabel"
)

// testPrint sends the built-in test label through the running agent's
// /print, so that its result is the agent's own: the same printer, the same
// errors a page would get.
func testPrint(e env) error {
	cfg, _, err := loadConfig(e, "", false)
	if err != nil {
		return err
	}
	size, err := testLabelSize(e, cfg)
	if err != nil {
		return err
	}
	agent, err := findAgent(e, cfg.Port)
	if err != nil {
		return err
	}
	if agent == nil {
		return agentNotRunning()
	}
	return sendTestLabel(e, cfg, size)
}

// testLabelSize is the size the test label is made for: --size, or else the
// configured printer's.
func testLabelSize(e env, cfg config.Config) (testlabel.Size, error) {
	if s, ok := e.flags["size"]; ok {
		size, err := testlabel.ParseSize(s)
		if err != nil {
			return size, usageError("--size " + err.Error())
		}
		return size, nil
	}
	return labelSize(cfg), nil
}

// sendTestLabel posts the test label to the agent running on the configured port.
func sendTestLabel(e env, cfg config.Config, size testlabel.Size) error {
	if len(cfg.Origins) == 0 {
		return labelNotSent("config.json allows no origin to print", "Add the page that prints: printpipe config --origin <url>")
	}

	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, agentURL(cfg.Port)+"/print", bytes.NewReader(testlabel.TSPL(size, Version, e.sys.Now())))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	// The agent lets only its origins print, and the CLI is none of them: it
	// prints as the first page config.json allows.
	req.Header.Set("Origin", cfg.Origins[0])
	resp, err := e.sys.Do(req)
	if err != nil {
		// The agent answered /health a moment ago, so it may not run now:
		// no "The agent is running." here.
		return &failure{msg: "the test label was not sent: the agent did not answer", hints: []string{"Check the agent: printpipe status"}}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		_, err := fmt.Fprintln(e.stdout, "A test label should have come out of the printer")
		return err
	}
	var answer struct{ Code, Message string }
	_ = json.NewDecoder(resp.Body).Decode(&answer)
	switch answer.Code {
	case "printer_not_found":
		return labelNotSent("the printer was not found", "Check that the printer is plugged in and switched on, then: printpipe test-print")
	case "printer_ambiguous":
		return labelNotSent("several printers match the configured device", chooseMatching(e, cfg.Device)...)
	case "printer_busy":
		return labelNotSent("another program is using the printer", "Close that program, then: printpipe test-print")
	case "printer_not_responding":
		return labelNotSent("the printer did not respond", "Check the paper, the cover and the power, then: printpipe test-print")
	case "origin_not_allowed":
		return labelNotSent("the agent does not allow the configured origin", "Restart the agent so that it reads config.json: printpipe restart")
	case "print_failed":
		if printer.IsNoPermission(answer.Message) {
			return labelNotSent(answer.Message, "sudo usermod -aG lp $USER, then log out and back in.")
		}
		return labelNotSent("printing failed", "The agent said: "+answer.Message, "Fix that, then: printpipe test-print")
	case "":
		return labelNotSent(fmt.Sprintf("the agent answered %s", resp.Status))
	default:
		return labelNotSent("the agent refused it", "The agent said: "+answer.Message)
	}
}

// chooseMatching is a command for each connected printer that matches the
// device, as install gives them. config --device restarts a running agent, so
// the test label can follow. When they cannot be listed, or fewer than two match
// by now, it says how to make the command.
func chooseMatching(e env, device string) []string {
	placeholder := []string{"Choose one with printpipe config --device usb:VID:PID:<serial>, then: printpipe test-print"}
	id, err := printer.ParseUSB(device)
	if err != nil {
		return placeholder
	}
	usb, err := printer.ListUSB(e.ctx, e.sys)
	if err != nil {
		return placeholder
	}
	match := slices.DeleteFunc(usb, func(p printer.USBPrinter) bool { return !id.Matches(p.USBID) })
	if len(match) < 2 {
		return placeholder
	}
	return chooseAmong(configDevice, match, "Then: printpipe test-print")
}

// labelSize is the test label's size for the configured printer: its known
// model's, else the default model's.
func labelSize(c config.Config) testlabel.Size {
	if m, ok := models.ForDevice(c.Device); ok {
		return m.TestLabelSize
	}
	return models.Default().TestLabelSize
}

// labelNotSent is the test label failing with the agent up: hints say what
// to do about the cause, and the last one that the agent still runs.
func labelNotSent(cause string, hints ...string) error {
	return &failure{msg: "the test label was not sent: " + cause, hints: append(hints, "The agent is running.")}
}
