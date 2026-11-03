package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgred/printpipe/internal/printer"
)

const app = "https://app.example"

func newHandler(t *testing.T, p Printer) http.Handler {
	t.Helper()
	return Handler(Options{Version: "1.2.3", PID: 4242, Origins: []string{app, "http://localhost:5173"}}, p)
}

func do(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestHealthNamesTheAgent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := do(newHandler(t, nil), r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d; body: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	want := map[string]any{"name": "printpipe", "version": "1.2.3", "pid": float64(4242)}
	if len(body) != len(want) {
		t.Errorf("body %s, want exactly %v", rec.Body, want)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s: %v, want %v", k, body[k], v)
		}
	}
}

// Any page may read /health, so that a client can tell the agent is running
// even before it is allowed to print.
func TestHealthAllowsTheRequestingOrigin(t *testing.T) {
	for _, origin := range []string{app, "https://other.example"} {
		r := httptest.NewRequest(http.MethodGet, "/health", nil)
		r.Header.Set("Origin", origin)
		rec := do(newHandler(t, nil), r)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("Access-Control-Allow-Origin %q, want %q", got, origin)
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary %q, want Origin", got)
		}
	}
}

// label is a job like a web app makes it: CP1251 text and n copies.
func label(n string) []byte {
	return append([]byte("SIZE 58 mm, 40 mm\r\nCLS\r\nTEXT 1,1,\"2\",0,1,1,\"\xc0\xf0\xf2. 000001\"\r\n"),
		"PRINT "+n+",1\r\n"...)
}

func printRequest(origin string, job []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/print", bytes.NewReader(job))
	r.Header.Set("Content-Type", "application/octet-stream")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestDryRunWritesTheJobAsReceived(t *testing.T) {
	path := filepath.Join(t.TempDir(), "label.tspl")
	h := newHandler(t, printer.File{Path: path})
	job := label("3")

	rec := do(h, printRequest(app, job))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d; body: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != app {
		t.Errorf("Access-Control-Allow-Origin %q, want %q", got, app)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, job) {
		t.Errorf("file %q, want %q", got, job)
	}

	// The next job replaces the last one rather than adding to it.
	second := label("1")
	do(h, printRequest(app, second))
	if got, _ := os.ReadFile(path); !bytes.Equal(got, second) {
		t.Errorf("after the second job the file is %q, want %q", got, second)
	}
}

// recorder is a printer that keeps the jobs it got.
type recorder struct{ jobs [][]byte }

func (p *recorder) Print(_ context.Context, job []byte) error {
	p.jobs = append(p.jobs, job)
	return nil
}

// A foreign page cannot print, but it can read why, so that its developer
// sees the origin is not configured rather than a network failure.
func TestForeignOriginCannotPrint(t *testing.T) {
	for _, origin := range []string{"https://evil.example", "http://localhost:3000", "null", ""} {
		t.Run(origin, func(t *testing.T) {
			p := &recorder{}
			rec := do(newHandler(t, p), printRequest(origin, label("1")))
			checkError(t, rec, origin, http.StatusForbidden, "origin_not_allowed")
			if len(p.jobs) != 0 {
				t.Errorf("printed %q", p.jobs)
			}
		})
	}
}

func preflight(origin string) *http.Request {
	r := httptest.NewRequest(http.MethodOptions, "/print", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "content-type")
	r.Header.Set("Access-Control-Request-Private-Network", "true")
	return r
}

// A POST of application/octet-stream is preflighted, so every page passes the
// preflight: otherwise a foreign page could not read the 403 of its POST.
func TestPreflightLetsEveryPageReadTheAnswer(t *testing.T) {
	h := newHandler(t, &recorder{})
	for _, origin := range []string{"http://localhost:5173", "https://evil.example"} {
		rec := do(h, preflight(origin))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status %d; body: %s", origin, rec.Code, rec.Body)
		}
		for name, want := range map[string]string{
			"Access-Control-Allow-Origin":          origin,
			"Access-Control-Allow-Methods":         "POST",
			"Access-Control-Allow-Headers":         "Content-Type",
			"Access-Control-Allow-Private-Network": "",
		} {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("%s: %s %q, want %q", origin, name, got, want)
			}
		}
	}
}

func TestJobPrintsOneToTenLabels(t *testing.T) {
	head := "SIZE 58 mm, 40 mm\r\nCLS\r\n"
	for _, tc := range []struct {
		name string
		job  string
		ok   bool
	}{
		{"one", head + "PRINT 1,1\r\n", true},
		{"ten", head + "PRINT 10,1\r\n", true},
		{"copies of a set", head + "PRINT 2,5\r\n", true},
		{"no copy count of a set", head + "PRINT 4\r\n", true},
		{"lower case, LF only", head + "print 3, 2\n", true},
		{"zero", head + "PRINT 0,1\r\n", false},
		{"eleven", head + "PRINT 11,1\r\n", false},
		{"sets times copies over ten", head + "PRINT 4,3\r\n", false},
		{"two PRINTs over ten", head + "PRINT 6,1\r\nCLS\r\nPRINT 5,1\r\n", false},
		{"no PRINT", head, false},
		{"not a number", head + "PRINT x,1\r\n", false},
		{"huge", head + "PRINT 99999999999999999999,1\r\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recorder{}
			rec := do(newHandler(t, p), printRequest(app, []byte(tc.job)))
			if tc.ok {
				if rec.Code != http.StatusNoContent || len(p.jobs) != 1 {
					t.Errorf("status %d, %d jobs; want 204 and 1 job; body: %s", rec.Code, len(p.jobs), rec.Body)
				}
				return
			}
			checkError(t, rec, app, http.StatusUnprocessableEntity, "bad_job")
			if len(p.jobs) != 0 {
				t.Errorf("printed %q", p.jobs)
			}
		})
	}
}

func TestOversizedJobIsRefused(t *testing.T) {
	p := &recorder{}
	job := append(bytes.Repeat([]byte("CLS\r\n"), 20000), "PRINT 1,1\r\n"...)
	rec := do(newHandler(t, p), printRequest(app, job))
	checkError(t, rec, app, http.StatusRequestEntityTooLarge, "bad_job")
	if len(p.jobs) != 0 {
		t.Errorf("printed %q", p.jobs)
	}
}

func TestEmptyJobIsRefused(t *testing.T) {
	p := &recorder{}
	rec := do(newHandler(t, p), printRequest(app, nil))
	checkError(t, rec, app, http.StatusBadRequest, "bad_job")
	if len(p.jobs) != 0 {
		t.Errorf("printed %q", p.jobs)
	}
}

// failing is a printer whose every job fails with err.
type failing struct{ err error }

func (p failing) Print(context.Context, []byte) error { return p.err }

// apiError is the body of every error answer.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// checkError checks an error answer: its status, its code, a message for
// people, and that the page may read it.
func checkError(t *testing.T, rec *httptest.ResponseRecorder, origin string, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status %d, want %d; body: %s", rec.Code, status, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	var body apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	if body.Code != code {
		t.Errorf("code %q, want %q", body.Code, code)
	}
	if body.Message == "" {
		t.Errorf("no message: %s", rec.Body)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("Access-Control-Allow-Origin %q, want %q", got, origin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "" {
		t.Errorf("Access-Control-Allow-Private-Network %q, want none", got)
	}
}

func TestPrinterFailuresHaveCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("usb:1FC9:2016: %w", printer.ErrNotFound), http.StatusBadGateway, "printer_not_found"},
		{fmt.Errorf("two XP-365B: %w", printer.ErrAmbiguous), http.StatusBadGateway, "printer_ambiguous"},
		{fmt.Errorf("/dev/usb/lp0: %w", printer.ErrBusy), http.StatusBadGateway, "printer_busy"},
		{fmt.Errorf("usb:1FC9:2016: %w", printer.ErrNotResponding), http.StatusGatewayTimeout, "printer_not_responding"},
		{errors.New("lp: something went wrong"), http.StatusBadGateway, "print_failed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			rec := do(newHandler(t, failing{tc.err}), printRequest(app, label("1")))
			checkError(t, rec, app, tc.status, tc.code)
		})
	}
}
