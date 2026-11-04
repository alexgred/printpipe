package main

import (
	"bytes"
	"os"
	"testing"
)

// The linker takes the manifest from the .syso, not from the file people read
// and edit: make manifest is what brings them together.
func TestTheWindowsResourceHoldsTheManifest(t *testing.T) {
	manifest, err := os.ReadFile("printpipe.exe.manifest")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(manifest, []byte(">detached</consoleAllocationPolicy>")) {
		t.Error("the manifest does not keep Windows from giving the program a console")
	}
	resource, err := os.ReadFile("rsrc_windows_amd64.syso")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(resource, manifest) {
		t.Error("rsrc_windows_amd64.syso is out of date: run make manifest")
	}
}
