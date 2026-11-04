//go:build !windows

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/alexgred/printpipe/internal/printer"
	"github.com/alexgred/printpipe/internal/server"
)

const testJob = "SIZE 58 mm, 40 mm\r\nCLS\r\nPRINT 1,1\r\n"

// printJob posts testJob to the agent and returns the status and the error
// code and message.
func printJob(t *testing.T, srv running) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.url+"/print", strings.NewReader(testJob))
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var answer struct{ Code, Message string }
	_ = json.NewDecoder(resp.Body).Decode(&answer)
	return resp.StatusCode, answer.Code, answer.Message
}

func TestAQueuePrintsTheJobUntouchedWithLpRaw(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "label"}`)
	sys.programs["lp"] = func([]string) ([]byte, error) { return []byte("request id is label-1 (1 file(s))\n"), nil }
	srv := startServer(t, sys)

	if status, code, _ := printJob(t, srv); status != http.StatusNoContent {
		t.Fatalf("/print: %d %s, want 204", status, code)
	}
	if len(sys.ran) == 0 {
		t.Fatal("ran no program, want lp")
	}
	if got := sys.ran[0]; !strings.HasPrefix(got.command, "lp -d label -o raw") || string(got.stdin) != testJob {
		t.Errorf("ran %q with stdin %q, want lp -d label -o raw with the job", got.command, got.stdin)
	}
}

func TestAQueueFailingAnswersWithItsPrintCode(t *testing.T) {
	refused := func([]string) ([]byte, error) {
		return nil, errors.New("lp: Error - The printer or class does not exist.")
	}
	for _, tc := range []struct {
		name     string
		programs map[string]func([]string) ([]byte, error)
		code     string
	}{
		{
			"the queue is not there",
			map[string]func([]string) ([]byte, error){
				"lp":     refused,
				"lpstat": func([]string) ([]byte, error) { return []byte("office\n"), nil },
			},
			"printer_not_found",
		},
		{
			"the queue is there and refuses the job",
			map[string]func([]string) ([]byte, error){
				"lp":     refused,
				"lpstat": func([]string) ([]byte, error) { return []byte("office\nLABEL\n"), nil },
			},
			"print_failed",
		},
		{
			"whether the queue is there cannot be told",
			map[string]func([]string) ([]byte, error){
				"lp":     refused,
				"lpstat": func([]string) ([]byte, error) { return nil, errors.New("lpstat failed") },
			},
			"print_failed",
		},
		{
			"the scheduler is down, so the list of queues is empty",
			map[string]func([]string) ([]byte, error){
				"lp": refused,
				"lpstat": func(args []string) ([]byte, error) {
					if args[0] == "-r" {
						return []byte("scheduler is not running\n"), nil
					}
					return nil, nil
				},
			},
			"print_failed",
		},
		{"CUPS is not installed", nil, "print_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "label"}`)
			if tc.programs != nil {
				sys.programs = tc.programs
			}
			srv := startServer(t, sys)
			if status, code, message := printJob(t, srv); status != http.StatusBadGateway || code != tc.code {
				t.Errorf("/print: %d %s %q, want 502 %s", status, code, message, tc.code)
			}
		})
	}
}

func TestADeviceFileGetsTheJobUntouched(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "device": "/dev/usb/lp0"}`)
	srv := startServer(t, sys)
	if status, code, _ := printJob(t, srv); status != http.StatusNoContent {
		t.Fatalf("/print: %d %s, want 204", status, code)
	}
	if got := string(sys.written["/dev/usb/lp0"]); got != testJob {
		t.Errorf("device file got %q, want %q", got, testJob)
	}
}

func TestADeviceFileFailingAnswersWithItsPrintCode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"no such file", fs.ErrNotExist, 502, "printer_not_found", "printer /dev/usb/lp0: printer not found"},
		{"unplugged while writing", syscall.ENODEV, 502, "printer_not_found", "printer /dev/usb/lp0: printer not found"},
		{"held by another program", syscall.EBUSY, 502, "printer_busy", "printer /dev/usb/lp0: printer busy"},
		{"write not done in time", context.DeadlineExceeded, 504, "printer_not_responding", "printer /dev/usb/lp0: printer not responding"},
		{"no permission", syscall.EACCES, 502, "print_failed", "no permission to write to /dev/usb/lp0"},
		{"any other error", syscall.EIO, 502, "print_failed", "printer /dev/usb/lp0: open /dev/usb/lp0: input/output error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "device": "/dev/usb/lp0"}`)
			sys.deviceErr["/dev/usb/lp0"] = &fs.PathError{Op: "open", Path: "/dev/usb/lp0", Err: tc.err}
			srv := startServer(t, sys)
			status, code, message := printJob(t, srv)
			if status != tc.status || code != tc.code || message != tc.message {
				t.Errorf("/print: %d %s %q, want %d %s %q", status, code, message, tc.status, tc.code, tc.message)
			}
		})
	}
}

func TestTestPrintTellsHowToGetPermissionForADeviceFile(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "device": "/dev/usb/lp0"}`)
	sys.deviceErr["/dev/usb/lp0"] = syscall.EACCES
	sys.onPort = server.Handler(server.Options{Version: "dev", PID: 4242, Origins: []string{"https://app.example"}}, printer.Device{Path: "/dev/usb/lp0", Computer: sys})

	got := run(t, sys, "test-print")

	want := "error: the test label was not sent: no permission to write to /dev/usb/lp0\n" +
		"  sudo usermod -aG lp $USER, then log out and back in.\n" +
		"  The agent is running.\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("got %+v, want exit 1 and\n%s", got, want)
	}
}
