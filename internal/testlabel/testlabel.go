// Package testlabel is printpipe's built-in test label: the one label the
// print agent makes itself, to check its connection to the printer, the roll
// size and Cyrillic. It needs no page, no network and no server.
package testlabel

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Size is a label's width and height in millimetres.
type Size struct {
	Width, Height float64
}

// maxSide is far above any label roll, so that a typo such as 580 is caught.
const maxSide = 300

func (s Size) String() string { return mm(s.Width) + "x" + mm(s.Height) }

// ParseSize reads W,H (or WxH) in millimetres, such as 58,40.
func ParseSize(s string) (Size, error) {
	bad := fmt.Errorf("%q is not W,H in mm, such as 58,40", s)
	w, h, ok := strings.Cut(s, ",")
	if !ok {
		w, h, ok = strings.Cut(s, "x")
	}
	if !ok {
		return Size{}, bad
	}
	var size Size
	for _, side := range []struct {
		text string
		v    *float64
	}{{w, &size.Width}, {h, &size.Height}} {
		v, err := strconv.ParseFloat(strings.TrimSpace(side.text), 64)
		if err != nil || !(v > 0 && v <= maxSide) {
			return Size{}, bad
		}
		*side.v = v
	}
	return size, nil
}

// cyrillic checks the font: the XP-365B prints Cyrillic only in font 3. Ё
// and ё lie outside CP1251's run of А to я, so they are checked too.
const cyrillic = "Кириллица Ёё Щщ Яя"

// TSPL is the test label for a roll of the given size: printpipe's name, the
// version and the date and time in English, and one Cyrillic line in font 3,
// in CP1251 with \r\n line ends.
//
// The layout fits a roll 40 mm wide and 20 mm high (at 203 dpi): font 2 is 12
// dots a character and font 3 is 16. It sets no GAP, so that the printer
// keeps the gap it calibrated itself.
func TSPL(size Size, version string, at time.Time) []byte {
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\r\n", a...) }
	text := func(y int, font, s string) { line(`TEXT 24,%d,"%s",0,1,1,"%s"`, y, font, s) }
	line("SIZE %s mm, %s mm", mm(size.Width), mm(size.Height))
	line("DIRECTION 1")
	line("CODEPAGE 1251")
	line("CLS")
	text(16, "2", "printpipe test print")
	// A quote would end the TSPL string.
	text(48, "2", "version "+strings.ReplaceAll(version, `"`, "'"))
	text(80, "2", at.Format("2006-01-02T15:04:05"))
	text(120, "3", cyrillic)
	line("PRINT 1,1")
	return cp1251(b.String())
}

func mm(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// cp1251 encodes s in Windows-1251. A character it has no byte for (only
// possible in a version string) becomes '?'.
func cp1251(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r >= 'А' && r <= 'я':
			out = append(out, byte(0xC0+r-'А'))
		case r == 'Ё':
			out = append(out, 0xA8)
		case r == 'ё':
			out = append(out, 0xB8)
		default:
			out = append(out, '?')
		}
	}
	return out
}
