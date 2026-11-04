package printer

import (
	"context"
	"errors"
	"strings"
)

// QueueInfo is what CUPS says of a queue.
type QueueInfo struct {
	Name string
	// DeviceURI is where the queue prints to, such as
	// usb://Xprinter/XP-365B?serial=1.
	DeviceURI string
	// MakeAndModel is the printer's description in its driver.
	MakeAndModel string
	// Accepting is whether the queue takes jobs now.
	Accepting bool
}

// Queues is the CUPS queues, read without root. Their options come from
// lpoptions -p and not from lpstat -v, whose text is localized. A queue whose
// options cannot be read has none, such as one removed meanwhile.
func Queues(ctx context.Context, c Computer) ([]QueueInfo, error) {
	names, err := queueNames(ctx, c)
	if err != nil {
		return nil, err
	}
	queues := make([]QueueInfo, len(names))
	for i, name := range names {
		queues[i].Name = name
		out, err := c.Run(ctx, nil, "lpoptions", "-p", name)
		if err != nil {
			continue
		}
		options := parseOptions(string(out))
		queues[i].DeviceURI = options["device-uri"]
		queues[i].MakeAndModel = options["printer-make-and-model"]
		queues[i].Accepting = options["printer-is-accepting-jobs"] == "true"
	}
	return queues, nil
}

// errSchedulerDown is CUPS being installed but its scheduler not running.
var errSchedulerDown = errors.New("the CUPS scheduler is not running")

// queueNames is the CUPS queues lpstat -e lists. With the scheduler down
// lpstat -e lists nothing and exits 0 like a computer without queues, and
// lpstat -r exits 0 too, so only its words (Computer runs programs in English)
// tell that apart.
func queueNames(ctx context.Context, c Computer) ([]string, error) {
	scheduler, err := c.Run(ctx, nil, "lpstat", "-r")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(scheduler)) == "scheduler is not running" {
		return nil, errSchedulerDown
	}
	out, err := c.Run(ctx, nil, "lpstat", "-e")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// parseOptions reads lpoptions' line of key=value and bare key words. A value
// may be quoted with ' or ", and \ escapes the character after it.
func parseOptions(line string) map[string]string {
	options := map[string]string{}
	var word strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if word.Len() > 0 {
			key, value, _ := strings.Cut(word.String(), "=")
			options[key] = value
			word.Reset()
		}
	}
	for _, r := range line {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			word.WriteRune(r)
		}
	}
	flush()
	return options
}
