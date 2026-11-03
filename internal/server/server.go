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
func Handler(o Options, p Printer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		// Any page may read it: a client tells the agent is running from it
		// even before it is allowed to print.
		h := w.Header()
		h.Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" {
			h.Set("Access-Control-Allow-Origin", origin)
		}
		h.Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			PID     int    `json:"pid"`
		}{"printpipe", o.Version, o.PID})
	})
	// Any page in the browser can call the agent; only the configured ones may,
	// and only they get CORS headers. A request without Origin did not come
	// from a page and is refused too.
	allow := func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if !slices.Contains(o.Origins, origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		return true
	}
	// The preflight of a POST with Content-Type: application/octet-stream.
	// Chrome asks a public page's request to loopback for the private
	// network permission as well.
	mux.HandleFunc("OPTIONS /print", func(w http.ResponseWriter, r *http.Request) {
		if !allow(w, r) {
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Methods", "POST")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			h.Set("Access-Control-Allow-Private-Network", "true")
		}
		h.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /print", func(w http.ResponseWriter, r *http.Request) {
		if !allow(w, r) {
			return
		}
		job, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJob))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "The job is larger than 64 KB.", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if n := copies(job); n < copiesMin || n > copiesMax {
			http.Error(w, fmt.Sprintf("A job prints %d to %d labels.", copiesMin, copiesMax), http.StatusUnprocessableEntity)
			return
		}
		if err := p.Print(r.Context(), job); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
