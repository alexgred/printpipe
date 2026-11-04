package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GitHub's /releases/latest and its release assets both redirect. The tag is read
// from the first answer, and an asset is at the end of its redirects.
func TestTheWebIsReadThroughRedirectsOnlyForDownloads(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/file", http.StatusFound)
	})
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the file"))
	})
	web := httptest.NewServer(mux)
	defer web.Close()
	ctx := context.Background()

	if got, err := (OS{}).Redirect(ctx, web.URL+"/moved"); err != nil || got != "/file" {
		t.Errorf("Redirect gave %q, %v, want /file", got, err)
	}
	var status httpStatus
	if _, err := (OS{}).Redirect(ctx, web.URL+"/file"); !errors.As(err, &status) || status != http.StatusOK {
		t.Errorf("Redirect of no redirect gave %v, want HTTP 200", err)
	}
	if got, err := (OS{}).Download(ctx, web.URL+"/moved"); err != nil || string(got) != "the file" {
		t.Errorf("Download gave %q, %v, want the file", got, err)
	}
	if _, err := (OS{}).Download(ctx, web.URL+"/none"); !errors.As(err, &status) || status != http.StatusNotFound {
		t.Errorf("Download of nothing gave %v, want HTTP 404", err)
	}
}
