package testlabel

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The golden files are written by hand, in CP1251 with \r\n, the bytes the
// printer must get.
func TestTSPLMatchesTheGoldenFilePerSize(t *testing.T) {
	at := time.Date(2026, 11, 3, 20, 15, 4, 0, time.FixedZone("MSK", 3*60*60))
	for _, size := range []Size{{58, 40}, {40, 30}} {
		t.Run(size.String(), func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata", size.String()+".tspl"))
			if err != nil {
				t.Fatal(err)
			}
			if got := TSPL(size, "0.2.0", at); !bytes.Equal(got, want) {
				t.Errorf("got\n%q\nwant\n%q", got, want)
			}
		})
	}
}

func TestParseSizeReadsWidthAndHeightInMillimetres(t *testing.T) {
	for in, want := range map[string]Size{"58,40": {58, 40}, "40x30": {40, 30}, "57.5,30": {57.5, 30}, " 30 , 20 ": {30, 20}} {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "58", "58,40,1", "a,b", "0,40", "58,-1", "1000,40", "58,40mm"} {
		if got, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) = %v, want an error", in, got)
		}
	}
}

func TestSizeStringNamesTheGoldenFile(t *testing.T) {
	if got := (Size{57.5, 30}).String(); got != "57.5x30" {
		t.Errorf("got %q, want 57.5x30", got)
	}
}
