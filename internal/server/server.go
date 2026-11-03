// Package server is printpipe's HTTP side: a web page in the browser on the
// same computer posts a label's TSPL to it, and the server hands the bytes to
// a printer as they are.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/alexgred/printpipe/internal/printer"
)

// Printer takes a print job, the bytes exactly as the printer gets them.
type Printer interface {
	Print(ctx context.Context, job []byte) error
}

// Options configure the handler.
type Options struct {
	// Version and PID are what /health reports.
	Version string
	PID     int
	// Origins are the pages allowed to print, such as https://app.example
	// and http://localhost:5173.
	Origins []string
}

// maxJob is far above a label's few hundred bytes.
const maxJob = 64 << 10

// Handler serves GET /health and POST /print.
//
// Every answer carries CORS headers for the requesting page, errors included,
// so that a page can always read why it did not print. Only the configured
// origins may print; that is checked by POST /print itself, not by CORS.
func Handler(o Options, p Printer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			PID     int    `json:"pid"`
		}{"printpipe", o.Version, o.PID})
	})
	// The preflight of a POST with Content-Type: application/octet-stream. It
	// passes for any page, or a foreign page could not read the 403 of its
	// POST.
	mux.HandleFunc("OPTIONS /print", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Methods", "POST")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /print", func(w http.ResponseWriter, r *http.Request) {
		// Any page in the browser can call the agent; only the configured ones
		// may print. A request without Origin did not come from a page and is
		// refused too.
		if origin := r.Header.Get("Origin"); !slices.Contains(o.Origins, origin) {
			fail(w, http.StatusForbidden, "origin_not_allowed", fmt.Sprintf("The origin %q is not allowed to print.", origin))
			return
		}
		job, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJob))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				fail(w, http.StatusRequestEntityTooLarge, "bad_job", "The job is larger than 64 KB.")
				return
			}
			fail(w, http.StatusBadRequest, "bad_job", "The job could not be read: "+err.Error())
			return
		}
		if len(job) == 0 {
			fail(w, http.StatusBadRequest, "bad_job", "The job is empty.")
			return
		}
		if n := copies(job); n < copiesMin || n > copiesMax {
			fail(w, http.StatusUnprocessableEntity, "bad_job", fmt.Sprintf("A job prints %d to %d labels.", copiesMin, copiesMax))
			return
		}
		if err := p.Print(r.Context(), job); err != nil {
			printFailed(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" {
			h.Set("Access-Control-Allow-Origin", origin)
		}
		mux.ServeHTTP(w, r)
	})
}

// fail answers with an error a page can read: a code for the page to act on
// and an English message for people.
func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message})
}

// printFailed answers with the code of the printer's failure.
func printFailed(w http.ResponseWriter, err error) {
	status, code := http.StatusBadGateway, "print_failed"
	switch {
	case errors.Is(err, printer.ErrNotFound):
		code = "printer_not_found"
	case errors.Is(err, printer.ErrAmbiguous):
		code = "printer_ambiguous"
	case errors.Is(err, printer.ErrBusy):
		code = "printer_busy"
	case errors.Is(err, printer.ErrNotResponding):
		status, code = http.StatusGatewayTimeout, "printer_not_responding"
	}
	fail(w, status, code, err.Error())
}
