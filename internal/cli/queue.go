package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/models"
	"github.com/alexgred/printpipe/internal/printer"
)

// install is, so far, only its printer step on macOS and Linux; the steps
// around it, and Windows, come with the rest of the command.
func install(e env) error {
	if e.sys.GOOS() == "windows" {
		return &failure{msg: "printpipe install is not implemented yet on this OS"}
	}
	cfg, path, err := loadConfig(e, "", false)
	if err != nil {
		return err
	}
	if cfg.Device != "" {
		return e.say("Printer: device %s", cfg.Device)
	}
	queues, err := printer.Queues(e.ctx, e.sys)
	if errors.Is(err, exec.ErrNotFound) {
		return noCUPS()
	}
	if err != nil {
		return &failure{msg: "could not read the printer queues: " + err.Error(), hints: []string{"Check that CUPS is running and run the same command again."}}
	}
	if hasQueue(queues, cfg.Queue) {
		return e.say("Printer: queue %s", cfg.Queue)
	}

	next, err := chooseQueue(e, cfg, path, queues)
	if err != nil {
		return err
	}
	if err := saveConfig(e, path, next); err != nil {
		return err
	}
	return e.say("Printer: queue %s", next.Queue)
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
	model, connected, err := connectedModel(e)
	if err != nil {
		return cfg, err
	}
	if connected {
		return createQueue(e, cfg, path, queues, model)
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

// createQueue makes a queue for the known model that is connected, with the
// one elevation printpipe ever asks for, and returns the configuration with it.
// Only system commands run as root, never printpipe itself.
func createQueue(e env, cfg config.Config, path string, queues []printer.QueueInfo, model models.Model) (config.Config, error) {
	name := queueName(model.Name, queues, cfg.CreatedQueue)
	if err := e.say("Creating the printer queue %s; sudo may ask for your computer password.", name); err != nil {
		return cfg, err
	}
	uris, err := printer.USBDeviceURIs(e.ctx, e.sys)
	if err != nil {
		return cfg, createFailed(err)
	}
	uri, err := modelURI(model, uris)
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
	cfg.Queue, cfg.CreatedQueue = name, name
	return cfg, nil
}

// connectedModel is the one known printer that is plugged in, false when there
// is none. It needs no root, so that no password is asked for when there is
// nothing to create a queue for.
func connectedModel(e env) (models.Model, bool, error) {
	usb, err := printer.ListUSB(e.ctx, e.sys)
	if err != nil {
		return models.Model{}, false, fmt.Errorf("could not list the USB printers: %w", err)
	}
	var found []models.Model
	for _, p := range usb {
		if m, ok := models.ByUSB(p.VID, p.PID); ok {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return models.Model{}, false, nil
	case 1:
		return found[0], true, nil
	default:
		return models.Model{}, false, severalConnected(len(found), found[0])
	}
}

// modelURI is the one device URI CUPS has for the model: printpipe never guesses
// which printer labels should go to.
func modelURI(model models.Model, uris []string) (string, error) {
	var match []string
	for _, uri := range uris {
		if model.PrintsTo(uri) {
			match = append(match, uri)
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
		return "", severalConnected(len(match), model)
	}
}

// severalConnected is several printers of a model being plugged in. The spec's
// printpipe config --device usb:… per printer works only on Windows.
func severalConnected(count int, model models.Model) error {
	return &failure{
		msg:   fmt.Sprintf("%d %s printers are connected", count, model.Name),
		hints: []string{"Leave one plugged in and run the same command again.", existingQueueHint},
	}
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
