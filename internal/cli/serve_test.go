package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const configPath = "/home/me/.config/printpipe/config.json"

// running is printpipe serve running in the background of a test.
type running struct {
	url  string
	stop func() result
}

// startServer runs printpipe serve with args until the test stops it.
func startServer(t *testing.T, sys *fakeSystem, args ...string) running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan result, 1)
	go func() { done <- runCtx(ctx, sys, append([]string{"serve"}, args...)...) }()
	select {
	case l := <-sys.listening:
		stop := func() result { cancel(); return <-done }
		t.Cleanup(func() { cancel() })
		return running{url: "http://" + l.Addr().String(), stop: stop}
	case got := <-done:
		cancel()
		t.Fatalf("serve exited before listening: %+v", got)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("serve did not listen")
	}
	panic("unreachable")
}

func health(t *testing.T, url, origin string) (map[string]any, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/health", nil)
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body, resp.Header
}

func TestServeAnswersHealthOnTheConfiguredPort(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"port": 9100, "origins": ["https://app.example"], "queue": "label"}`)
	srv := startServer(t, sys)

	if sys.listened != "127.0.0.1:9100" {
		t.Errorf("listened on %q, want 127.0.0.1:9100", sys.listened)
	}
	body, h := health(t, srv.url, "https://app.example")
	if body["name"] != "printpipe" || body["version"] != "dev" || body["pid"] != float64(os.Getpid()) {
		t.Errorf("/health %v, want printpipe, dev and pid %d", body, os.Getpid())
	}
	if got := h.Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin %q", got)
	}
	if got := srv.stop(); got.code != 0 {
		t.Errorf("stopped: %+v, want exit 0", got)
	}
}

func TestServeListensOnPort8631ByDefault(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "queue": "label"}`)
	startServer(t, sys)
	if sys.listened != "127.0.0.1:8631" {
		t.Errorf("listened on %q, want 127.0.0.1:8631", sys.listened)
	}
}

// --dry-run prints nowhere: each job lands in the file, and the config needs
// no printer for it.
func TestServeDryRunWritesJobsToTheFile(t *testing.T) {
	sys := newFakeSystem()
	sys.files["/dev/printpipe.json"] = []byte(`{"origins": ["http://localhost:5173"]}`)
	out := filepath.Join(t.TempDir(), "dry-run.tspl")
	srv := startServer(t, sys, "--config", "/dev/printpipe.json", "--dry-run", out)

	job := "SIZE 58 mm, 40 mm\r\nCLS\r\nPRINT 1,1\r\n"
	req, _ := http.NewRequest(http.MethodPost, srv.url+"/print", strings.NewReader(job))
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("/print status %d", resp.StatusCode)
	}
	if got, _ := os.ReadFile(out); string(got) != job {
		t.Errorf("dry run file %q, want %q", got, job)
	}
}

func TestServeRefusesAConfigItCannotWorkWith(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string // "" for no config file
		args   []string
		error  string
	}{
		{"no config file", "", nil, "error: " + configPath + ": no printer: set a queue or a device"},
		{"bad JSON", `{"port": "x"`, nil, "error: " + configPath + ": unexpected end of JSON input"},
		{"no printer", `{"origins": ["https://app.example"]}`, nil, "error: " + configPath + ": no printer: set a queue or a device"},
		{"queue and device", `{"origins": ["https://app.example"], "queue": "a", "device": "/dev/usb/lp0"}`, nil, "error: " + configPath + ": set either a queue or a device, not both"},
		{"bad USB device", `{"origins": ["https://app.example"], "device": "usb:1FC9"}`, nil, "error: " + configPath + `: device "usb:1FC9": not two or three parts; want usb:VID:PID or usb:VID:PID:serial, such as usb:1FC9:2016`},
		{"no origins", `{"queue": "a"}`, nil, "error: " + configPath + `: no "origins": no page could print`},
		{"missing --config file", "", []string{"--config", "/nowhere.json"}, "error: open /nowhere.json: file does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFakeSystem()
			if tc.config != "" {
				sys.files[configPath] = []byte(tc.config)
			}
			got := run(t, sys, append([]string{"serve"}, tc.args...)...)
			if got.code != 1 || got.stderr != tc.error+"\n" {
				t.Errorf("got %+v, want exit 1 and %q", got, tc.error)
			}
			if sys.listened != "" {
				t.Errorf("listened on %q", sys.listened)
			}
		})
	}
}

// A usb: device is a USB printer found by its IDs, not a device file.
func TestServePrintsToAUSBDevice(t *testing.T) {
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{"origins": ["https://app.example"], "device": "usb:1fc9:2016:A1"}`)
	srv := startServer(t, sys)
	got := srv.stop()
	if !strings.Contains(got.stderr, "printer: USB printer usb:1FC9:2016:A1,") {
		t.Errorf("log %q does not name the USB printer", got.stderr)
	}
}
