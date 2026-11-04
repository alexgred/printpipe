package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/models"
	"github.com/alexgred/printpipe/internal/printer"
)

// cupsPrinter is the configuration with the printer to print to on macOS and
// Linux: a saved device or queue that is still there, the one --device chooses,
// else what chooseQueue finds. Without one it fails and the caller keeps what
// it returns.
func cupsPrinter(e env, cfg config.Config, path string, chosen *printer.USBID) (config.Config, error) {
	if chosen == nil && cfg.Device != "" {
		return cfg, nil
	}
	queues, err := printer.Queues(e.ctx, e.sys)
	if errors.Is(err, exec.ErrNotFound) {
		return cfg, noCUPS()
	}
	if err != nil {
		return cfg, &failure{msg: "could not read the printer queues: " + err.Error(), hints: []string{"Check that CUPS is running and run the same command again."}}
	}
	if chosen != nil {
		return chooseDevice(e, cfg, path, queues, *chosen)
	}
	if hasQueue(queues, cfg.Queue) {
		return cfg, nil
	}
	// A queue named on the command line is no reason to print to another.
	if _, named := e.flags["queue"]; named {
		return cfg, &failure{
			msg:   fmt.Sprintf("there is no printer queue %s", cfg.Queue),
			hints: []string{"See the queues: lpstat -e", "Run the same command again with one of them."},
		}
	}
	return chooseQueue(e, cfg, path, queues)
}

// chooseDevice is the configuration with a queue for the printer --device names:
// one there is, else a new one.
func chooseDevice(e env, cfg config.Config, path string, queues []printer.QueueInfo, id printer.USBID) (config.Config, error) {
	model, ok := models.ByUSB(id.VID, id.PID)
	if !ok {
		return cfg, &failure{
			msg:   fmt.Sprintf("%s is not a printer model printpipe knows", id),
			hints: []string{"An existing queue: printpipe install --queue <name>"},
		}
	}
	for _, q := range models.UsableQueues(queues) {
		if model.PrintsTo(q.DeviceURI) && serialMatches(id, q.DeviceURI) {
			cfg.Queue, cfg.Device = q.Name, ""
			return cfg, nil
		}
	}
	known, err := connectedKnown(e)
	if err != nil {
		return cfg, err
	}
	known = slices.DeleteFunc(known, func(k knownPrinter) bool { return !id.Matches(k.id) })
	switch len(known) {
	case 0:
		return cfg, notConnected(id)
	case 1:
		return createQueue(e, cfg, path, queues, model, id)
	default:
		return cfg, severalConnected(e.sys.GOOS(), known)
	}
}

// notConnected is the printer named on the command line not being there: no
// reason to print to another.
func notConnected(id printer.USBID) error {
	return &failure{
		msg:   fmt.Sprintf("the printer %s is not connected", id),
		hints: []string{"Plug it in, switch it on and run the same command again."},
	}
}

// chooseQueue is the configuration with the queue to print to: a known model's
// that is there, else a new one for a known model that is connected, else a
// queue that only calls itself a label printer, which may print to anything.
func chooseQueue(e env, cfg config.Config, path string, queues []printer.QueueInfo) (config.Config, error) {
	usable := models.UsableQueues(queues)
	if len(usable) > 0 && models.PrintsToKnownModel(usable[0].DeviceURI) {
		cfg.Queue = usable[0].Name
		return cfg, nil
	}
	known, err := connectedKnown(e)
	if err != nil {
		return cfg, err
	}
	switch len(known) {
	case 0:
	case 1:
		return createQueue(e, cfg, path, queues, known[0].model, printer.USBID{VID: known[0].id.VID, PID: known[0].id.PID})
	default:
		return cfg, severalConnected(e.sys.GOOS(), known)
	}
	if len(usable) > 0 {
		cfg.Queue = usable[0].Name
		return cfg, nil
	}
	return cfg, &failure{
		msg: "no label printer queue and no known printer found",
		hints: []string{
			"Plug the " + models.Default().Name + " in, switch it on and run the same command again.",
			existingQueueHint,
		},
	}
}

// createQueue makes a queue for the known model that is connected, the printer
// id names, with the one elevation printpipe ever asks for, and returns the
// configuration with it. Only system commands run as root, never printpipe itself.
func createQueue(e env, cfg config.Config, path string, queues []printer.QueueInfo, model models.Model, id printer.USBID) (config.Config, error) {
	name := queueName(model.Name, queues, cfg.CreatedQueue)
	if err := e.say("Creating the printer queue %s; sudo may ask for your computer password.", name); err != nil {
		return cfg, err
	}
	uris, err := printer.USBDeviceURIs(e.ctx, e.sys)
	if err != nil {
		return cfg, createFailed(err)
	}
	uri, err := deviceURI(e.sys.GOOS(), model, id, uris)
	if err != nil {
		return cfg, err
	}
	// lpadmin reads the PPD as root and sends it to CUPS, so it can be gone after.
	ppd := filepath.Join(filepath.Dir(path), name+".ppd")
	if err := e.sys.WriteFile(ppd, model.PPD); err != nil {
		return cfg, err
	}
	defer func() { _ = e.sys.Remove(ppd) }()
	if err := printer.CreateQueue(e.ctx, e.sys, name, uri, ppd); err != nil {
		return cfg, createFailed(err)
	}
	cfg.Queue, cfg.Device, cfg.CreatedQueue = name, "", name
	return cfg, nil
}

// knownPrinter is a connected printer of a model printpipe knows.
type knownPrinter struct {
	model models.Model
	id    printer.USBID
}

// connectedKnown is the known printers that are plugged in. It needs no root, so
// that no password is asked for when there is nothing to create a queue for.
func connectedKnown(e env) ([]knownPrinter, error) {
	usb, err := printer.ListUSB(e.ctx, e.sys)
	if err != nil {
		return nil, fmt.Errorf("could not list the USB printers: %w", err)
	}
	return knownAmong(usb), nil
}

func knownAmong(usb []printer.USBPrinter) []knownPrinter {
	var known []knownPrinter
	for _, p := range usb {
		if m, ok := models.ByUSB(p.VID, p.PID); ok {
			known = append(known, knownPrinter{m, p.USBID})
		}
	}
	return known
}

// deviceURI is the one device URI CUPS has for the printer id names: printpipe
// never guesses which printer labels should go to.
func deviceURI(goos string, model models.Model, id printer.USBID, uris []string) (string, error) {
	var match []string
	var found []knownPrinter
	for _, uri := range uris {
		if model.PrintsTo(uri) && serialMatches(id, uri) {
			match = append(match, uri)
			found = append(found, knownPrinter{model, printer.USBID{VID: model.VID, PID: model.PID, Serial: strings.ToUpper(uriSerial(uri))}})
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return "", &failure{
			msg:   "CUPS does not list the connected " + model.Name,
			hints: []string{"Switch the printer off and on and run the same command again.", existingQueueHint},
		}
	default:
		return "", severalConnected(goos, found)
	}
}

// serialMatches is whether a device URI is of the printer id names, by its serial
// when id has one. USB IDs are not in a URI, which names the model instead.
func serialMatches(id printer.USBID, uri string) bool {
	return id.Serial == "" || strings.EqualFold(uriSerial(uri), id.Serial)
}

// uriSerial is the serial in a device URI such as
// usb://Xprinter/XP-365B?serial=1&interface=1; empty when it has none.
func uriSerial(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return u.Query().Get("serial")
}

// severalConnected is several known printers being plugged in, with a ready
// command for each. A usb: device is printed to only on Windows; elsewhere install
// makes a queue for the one it names. A printer with no serial cannot be named
// apart from one of its model.
func severalConnected(goos string, printers []knownPrinter) error {
	msg := fmt.Sprintf("%d known label printers are connected", len(printers))
	if !slices.ContainsFunc(printers, func(p knownPrinter) bool { return p.model.Name != printers[0].model.Name }) {
		msg = fmt.Sprintf("%d %s printers are connected", len(printers), printers[0].model.Name)
	}
	command := "printpipe install --device "
	if goos == "windows" {
		command = "printpipe config --device "
	}
	var hints []string
	noSerial := false
	for _, p := range printers {
		if p.id.Serial == "" && sameModelCount(printers, p) > 1 {
			noSerial = true
			continue
		}
		hints = append(hints, command+p.id.String())
	}
	if noSerial {
		hints = append(hints, "A printer with no serial number cannot be chosen among its model: leave one plugged in and run the same command again.")
	}
	return &failure{msg: msg, hints: hints}
}

func sameModelCount(printers []knownPrinter, p knownPrinter) int {
	count := 0
	for _, other := range printers {
		if other.id.VID == p.id.VID && other.id.PID == p.id.PID {
			count++
		}
	}
	return count
}

// queueName is the name for the queue to create: the one printpipe created
// before when it is still there, to repair it rather than leave it behind.
// lpadmin -p changes a queue that has the name, so any other queue keeps its own.
func queueName(base string, queues []printer.QueueInfo, created string) string {
	if hasQueue(queues, created) {
		return created
	}
	name := base
	for i := 2; hasQueue(queues, name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// hasQueue is whether a queue has the name, in which CUPS ignores case.
func hasQueue(queues []printer.QueueInfo, name string) bool {
	return slices.ContainsFunc(queues, func(q printer.QueueInfo) bool { return strings.EqualFold(q.Name, name) })
}

const existingQueueHint = "An existing queue: printpipe config --queue <name>"

func noCUPS() error {
	return &failure{msg: "CUPS is not installed (lpadmin not found)", hints: []string{"Install CUPS and run the same command again."}}
}

// createFailed tells apart why a command run through sudo failed, by who said
// so: sudo speaks English here, and lpinfo and lpadmin start their words with
// their names. Whatever else fails is sudo not letting the command run, in words
// that differ by version and by the reason.
func createFailed(err error) error {
	said := err.Error()
	switch {
	case strings.Contains(said, "lpinfo: command not found"), strings.Contains(said, "lpadmin: command not found"):
		return noCUPS()
	case errors.Is(err, exec.ErrNotFound), strings.Contains(said, "lpinfo:"), strings.Contains(said, "lpadmin:"):
		return &failure{msg: "could not create the printer queue: " + said, hints: []string{existingQueueHint}}
	}
	return &failure{
		msg:   "could not create the printer queue: administrator rights were refused",
		hints: []string{"Run the same command again and enter your computer password when asked.", existingQueueHint},
	}
}
