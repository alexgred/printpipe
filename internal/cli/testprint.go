package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/models"
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
	size := labelSize(cfg)
	if s, ok := e.flags["size"]; ok {
		if size, err = testlabel.ParseSize(s); err != nil {
			return usageError("--size " + err.Error())
		}
	}
	agentURL := "http://" + agentAddr(cfg)
	if err := findAgent(e, agentURL, cfg.Port); err != nil {
		return err
	}
	if len(cfg.Origins) == 0 {
		return labelNotSent("config.json allows no origin to print", "Add the page that prints: printpipe config --origin <url>")
	}

	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, agentURL+"/print", bytes.NewReader(testlabel.TSPL(size, Version, e.sys.Now())))
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
		return labelNotSent("several printers match the configured device", "Choose one with printpipe config --device usb:VID:PID:<serial>, then: printpipe test-print")
	case "printer_busy":
		return labelNotSent("another program is using the printer", "Close that program, then: printpipe test-print")
	case "printer_not_responding":
		return labelNotSent("the printer did not respond", "Check the paper, the cover and the power, then: printpipe test-print")
	case "origin_not_allowed":
		return labelNotSent("the agent does not allow the configured origin", "Restart the agent so that it reads config.json: printpipe restart")
	case "print_failed":
		return labelNotSent("printing failed", "The agent said: "+answer.Message, "Fix that, then: printpipe test-print")
	case "":
		return labelNotSent(fmt.Sprintf("the agent answered %s", resp.Status))
	default:
		return labelNotSent("the agent refused it", "The agent said: "+answer.Message)
	}
}

// labelSize is the test label's size for the configured printer: its known
// model's, else the default model's.
func labelSize(c config.Config) testlabel.Size {
	if m, ok := models.ForDevice(c.Device); ok {
		return m.TestLabelSize
	}
	return models.Default().TestLabelSize
}

// findAgent checks that printpipe answers on the port, so that nothing is
// ever sent to a program printpipe does not own.
func findAgent(e env, agentURL string, port int) error {
	req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, agentURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := e.sys.Do(req)
	var dial *net.OpError
	if errors.As(err, &dial) && dial.Op == "dial" {
		return &failure{msg: "the agent is not running", hints: []string{"Start it: printpipe start"}}
	}
	if err != nil {
		return portBusy(port)
	}
	defer resp.Body.Close()
	var health struct{ Name string }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&health) != nil || health.Name != "printpipe" {
		return portBusy(port)
	}
	return nil
}

func portBusy(port int) error {
	return &failure{
		msg: fmt.Sprintf("port %d is used by another program", port),
		hints: []string{
			"Close that program and run the same command again.",
			fmt.Sprintf("Windows: netstat -ano | findstr :%d   macOS/Linux: lsof -i :%d", port, port),
		},
	}
}

// labelNotSent is the test label failing with the agent up: hints say what
// to do about the cause, and the last one that the agent still runs.
func labelNotSent(cause string, hints ...string) error {
	return &failure{msg: "the test label was not sent: " + cause, hints: append(hints, "The agent is running.")}
}
