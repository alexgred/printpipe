package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alexgred/printpipe/internal/printer"
)

const app = "https://app.example"

// newPrinting is a computer with a configured XP-365B and the agent running.
func newPrinting(t *testing.T) (*fakeSystem, *fakePrinter) {
	t.Helper()
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"origins": ["` + app + `"], "device": "usb:1FC9:2016"}`)
	p := &fakePrinter{}
	sys.runAgent(p, app)
	return sys, p
}

func TestTestPrintSendsTheTestLabelThroughTheAgent(t *testing.T) {
	sys, p := newPrinting(t)
	got := run(t, sys, "test-print")
	if got.code != 0 || got.stdout != "A test label should have come out of the printer\n" || got.stderr != "" {
		t.Errorf("got %+v, want exit 0 and the test label line", got)
	}
	if len(p.jobs) != 1 {
		t.Fatalf("the printer got %d jobs, want 1", len(p.jobs))
	}
	job := p.jobs[0]
	for _, want := range []string{"SIZE 40 mm, 30 mm\r\n", `"printpipe test print"`, `"version dev"`, `"2026-11-03T20:15:04"`, "CODEPAGE 1251\r\n", "PRINT 1,1\r\n"} {
		if !bytes.Contains(job, []byte(want)) {
			t.Errorf("the job has no %q:\n%q", want, job)
		}
	}
	// "Кириллица" in CP1251, in font 3.
	if !bytes.Contains(job, []byte("\"3\",0,1,1,\"\xca\xe8\xf0\xe8\xeb\xeb\xe8\xf6\xe0")) {
		t.Errorf("the job has no Cyrillic line in font 3 in CP1251:\n%q", job)
	}
}

func TestTestPrintGoesToTheConfiguredPort(t *testing.T) {
	sys, _ := newPrinting(t)
	sys.files[configPath] = []byte(`{"port": 9100, "origins": ["` + app + `"], "queue": "label"}`)
	sys.agentPort = "9100"
	if got := run(t, sys, "test-print"); got.code != 0 {
		t.Fatalf("got %+v, want exit 0", got)
	}
	for _, u := range sys.requested {
		if !strings.HasPrefix(u, "http://127.0.0.1:9100/") {
			t.Errorf("requested %s, want 127.0.0.1:9100", u)
		}
	}
}

func TestTestPrintSizeOverridesTheModelsSize(t *testing.T) {
	sys, p := newPrinting(t)
	for _, args := range [][]string{{"--size", "40,30"}, {"--size=40x30"}} {
		p.jobs = nil
		if got := run(t, sys, append([]string{"test-print"}, args...)...); got.code != 0 {
			t.Fatalf("%q: got %+v, want exit 0", args, got)
		}
		if len(p.jobs) != 1 || !bytes.HasPrefix(p.jobs[0], []byte("SIZE 40 mm, 30 mm\r\n")) {
			t.Errorf("%q: jobs %q, want one 40x30 label", args, p.jobs)
		}
	}
}

// A queue or a device of a model printpipe does not know gets the label of
// the first known model, the XP-365B, unless --size says otherwise.
func TestTestPrintWithoutAKnownModelUsesTheDefaultSize(t *testing.T) {
	for _, printerConfig := range []string{`"queue": "label"`, `"device": "/dev/usb/lp0"`, `"device": "usb:0483:5740"`} {
		sys, p := newPrinting(t)
		sys.files[configPath] = []byte(`{"origins": ["` + app + `"], ` + printerConfig + `}`)
		if got := run(t, sys, "test-print"); got.code != 0 {
			t.Fatalf("%s: got %+v, want exit 0", printerConfig, got)
		}
		if len(p.jobs) != 1 || !bytes.HasPrefix(p.jobs[0], []byte("SIZE 40 mm, 30 mm\r\n")) {
			t.Errorf("%s: jobs %q, want one 40x30 label", printerConfig, p.jobs)
		}
	}
}

func TestTestPrintRefusesABadSize(t *testing.T) {
	sys, p := newPrinting(t)
	got := run(t, sys, "test-print", "--size", "58")
	if got.code != 2 || got.stderr != "error: --size \"58\" is not W,H in mm, such as 58,40\n  Run printpipe help to see the commands.\n" {
		t.Errorf("got %+v, want exit 2 and the size error", got)
	}
	if len(p.jobs) != 0 || len(sys.requested) != 0 {
		t.Errorf("sent %q to the agent", sys.requested)
	}
}

func TestTestPrintSaysWhyTheLabelWasNotSent(t *testing.T) {
	for _, tc := range []struct {
		err   error
		lines []string
	}{
		{fmt.Errorf("printer usb:1FC9:2016: %w", printer.ErrNotFound), []string{
			"error: the test label was not sent: the printer was not found",
			"  Check that the printer is plugged in and switched on, then: printpipe test-print",
		}},
		{fmt.Errorf("printer usb:1FC9:2016: %w", printer.ErrAmbiguous), []string{
			"error: the test label was not sent: several printers match the configured device",
			"  Choose one with printpipe config --device usb:VID:PID:<serial>, then: printpipe test-print",
		}},
		{fmt.Errorf("printer usb:1FC9:2016: %w", printer.ErrBusy), []string{
			"error: the test label was not sent: another program is using the printer",
			"  Close that program, then: printpipe test-print",
		}},
		{fmt.Errorf("printer usb:1FC9:2016: %w", printer.ErrNotResponding), []string{
			"error: the test label was not sent: the printer did not respond",
			"  Check the paper, the cover and the power, then: printpipe test-print",
		}},
		{fmt.Errorf("queue label: exit status 1: lp: Unauthorized"), []string{
			"error: the test label was not sent: printing failed",
			"  The agent said: queue label: exit status 1: lp: Unauthorized",
			"  Fix that, then: printpipe test-print",
		}},
	} {
		t.Run(tc.lines[0], func(t *testing.T) {
			sys, p := newPrinting(t)
			p.err = tc.err
			got := run(t, sys, "test-print")
			want := strings.Join(append(tc.lines, "  The agent is running."), "\n") + "\n"
			if got.code != 1 || got.stdout != "" || got.stderr != want {
				t.Errorf("got %+v\nwant exit 1 and stderr\n%s", got, want)
			}
		})
	}
}

// The agent still runs with an older configuration: it allows another
// origin than config.json now names.
func TestTestPrintWithAnOriginTheAgentDoesNotAllowSaysToRestart(t *testing.T) {
	sys, _ := newPrinting(t)
	sys.runAgent(&fakePrinter{}, "https://old.example")
	got := run(t, sys, "test-print")
	want := "error: the test label was not sent: the agent does not allow the configured origin\n" +
		"  Restart the agent so that it reads config.json: printpipe restart\n" +
		"  The agent is running.\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("got %+v\nwant exit 1 and stderr\n%s", got, want)
	}
}

func TestTestPrintWithTheAgentNotRunningFails(t *testing.T) {
	sys, _ := newPrinting(t)
	sys.onPort = nil
	got := run(t, sys, "test-print")
	want := "error: the agent is not running\n  Start it: printpipe start\n"
	if got.code != 1 || got.stdout != "" || got.stderr != want {
		t.Errorf("got %+v\nwant exit 1 and stderr\n%s", got, want)
	}
}

// printpipe never sends a label to a program it does not own.
func TestTestPrintToAForeignProgramOnThePortFails(t *testing.T) {
	sys, _ := newPrinting(t)
	sys.runForeignProgram()
	got := run(t, sys, "test-print")
	want := "error: port 8631 is used by another program\n" +
		"  Close that program and run the same command again.\n" +
		"  Windows: netstat -ano | findstr :8631   macOS/Linux: lsof -i :8631\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("got %+v\nwant exit 1 and stderr\n%s", got, want)
	}
	for _, u := range sys.requested {
		if strings.HasSuffix(u, "/print") {
			t.Errorf("posted the label to the foreign program")
		}
	}
}

// Without origins in config.json no page may print, the CLI's label
// neither; restarting would not help.
func TestTestPrintWithoutOriginsSaysToAddOne(t *testing.T) {
	sys, p := newPrinting(t)
	sys.files[configPath] = []byte(`{"device": "usb:1FC9:2016"}`)
	got := run(t, sys, "test-print")
	want := "error: the test label was not sent: config.json allows no origin to print\n" +
		"  Add the page that prints: printpipe config --origin <url>\n" +
		"  The agent is running.\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("got %+v\nwant exit 1 and stderr\n%s", got, want)
	}
	if len(p.jobs) != 0 {
		t.Errorf("printed %q", p.jobs)
	}
}

// An agent that answers /print with something the CLI cannot read.
func TestTestPrintWithAnAnswerItCannotReadSaysWhatCameBack(t *testing.T) {
	sys, _ := newPrinting(t)
	agent := sys.onPort
	sys.onPort = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/print" {
			http.Error(w, "oops", http.StatusInternalServerError)
			return
		}
		agent.ServeHTTP(w, r)
	})
	got := run(t, sys, "test-print")
	want := "error: the test label was not sent: the agent answered 500 Internal Server Error\n" +
		"  The agent is running.\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("got %+v\nwant exit 1 and stderr\n%s", got, want)
	}
}
