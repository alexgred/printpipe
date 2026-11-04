package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/alexgred/printpipe/internal/layout"
	"github.com/alexgred/printpipe/internal/printer"
)

// remove takes printpipe off the computer and can be run again at any time. A
// failing step leaves what comes after it, so that the installed binary, which
// goes last, can run the same command again.
func remove(e env) error {
	svc, err := serviceOf(e, "remove")
	if err != nil {
		return err
	}
	files, err := filesOf(e.sys)
	if err != nil {
		return err
	}
	cfg, _, err := loadConfig(e, "", false)
	if err != nil {
		return &failure{msg: err.Error(), hints: []string{"Correct or delete that file and run the same command again."}}
	}
	port, agent, err := findOwnAgent(e)
	if err != nil {
		return err
	}
	_, deleteQueue := e.flags["queue"]
	if deleteQueue {
		if err := deleteCreatedQueue(e, cfg.CreatedQueue); err != nil {
			return err
		}
	}
	if err := stopIfRunning(e, svc, port, agent); err != nil {
		return err
	}
	if err := svc.removeStartup(e); err != nil {
		return err
	}
	if err := removeFiles(e, files); err != nil {
		return err
	}
	if err := e.say("printpipe removed"); err != nil {
		return err
	}
	if !deleteQueue && cfg.CreatedQueue != "" {
		return e.say("Printer queue kept: %s. To delete it: sudo lpadmin -x %s", cfg.CreatedQueue, cfg.CreatedQueue)
	}
	return nil
}

// deleteCreatedQueue deletes the queue printpipe created, the only one it may. It
// comes before anything is removed: the config is what remembers the queue, and sudo
// may be refused.
func deleteCreatedQueue(e env, name string) error {
	if e.sys.GOOS() == "windows" {
		return e.say("printpipe creates no printer queue on Windows; none was deleted")
	}
	if name == "" {
		return e.say("printpipe created no printer queue here; none was deleted")
	}
	queues, err := printer.Queues(e.ctx, e.sys)
	if err != nil && !errors.Is(err, exec.ErrNotFound) {
		return &failure{msg: "could not read the printer queues: " + err.Error(), hints: []string{"Check that CUPS is running and run the same command again."}}
	}
	if !hasQueue(queues, name) {
		return e.say("The printer queue %s is already gone", name)
	}
	if err := e.say("Deleting the printer queue %s; sudo may ask for your computer password.", name); err != nil {
		return err
	}
	if err := printer.DeleteQueue(e.ctx, e.sys, name); err != nil {
		return deleteQueueFailed(name, err)
	}
	return e.say("Printer queue deleted: %s", name)
}

// deleteQueueFailed tells apart why, as createFailed does.
func deleteQueueFailed(name string, err error) error {
	said := err.Error()
	if errors.Is(err, exec.ErrNotFound) || strings.Contains(said, "lpadmin:") {
		return &failure{msg: fmt.Sprintf("could not delete the printer queue %s: %s", name, said)}
	}
	return &failure{
		msg:   fmt.Sprintf("could not delete the printer queue %s: administrator rights were refused", name),
		hints: []string{"Run the same command again and enter your computer password when asked."},
	}
}

// removeFiles removes printpipe's files and, on Windows, its folder in the user's
// Path. The config comes just before the program, as it is what remembers the queue
// when the same command is run again. On Windows, what a running program holds goes
// after this command: the program itself and, as an agent only just off its port may
// still have it open, the log. A folder that holds anything else stays.
func removeFiles(e env, files layout.Paths) error {
	logAndSwap := []string{files.Log, files.Log + ".1", files.NewBinary, files.OldBinary}
	config := []string{files.Config + ".tmp", files.Config}
	if e.sys.GOOS() == "windows" {
		if err := removeEach(e, config); err != nil {
			return err
		}
		_ = e.sys.Remove(files.ConfigDir)
		if err := removeFromUserPath(e, files.BinaryDir); err != nil {
			return err
		}
		if err := e.sys.RemoveAfterExit(append(logAndSwap, files.Binary), []string{files.BinaryDir, files.LogDir}); err != nil {
			return &failure{
				msg:   fmt.Sprintf("could not remove %s: %v", files.Binary, err),
				hints: []string{fmt.Sprintf(`Run the same command again once that is fixed: "%s" remove`, files.Binary)},
			}
		}
		return nil
	}
	if err := removeEach(e, slices.Concat(logAndSwap, config, []string{files.Binary})); err != nil {
		return err
	}
	_ = e.sys.Remove(files.ConfigDir)
	if files.LogDir != "" {
		_ = e.sys.Remove(files.LogDir)
	}
	return nil
}

// removeEach removes the files in order, and stops at one it cannot.
func removeEach(e env, files []string) error {
	for _, file := range files {
		if err := removeIfThere(e.sys, file); err != nil {
			return couldNotRemove(file, err)
		}
	}
	return nil
}

// removeFromUserPath takes the folder out of the user's Path, leaving every other
// entry as it is written, and a Path without it unwritten.
func removeFromUserPath(e env, folder string) error {
	current, err := e.sys.UserPath()
	if err != nil {
		return err
	}
	entries := strings.Split(current, ";")
	kept := slices.DeleteFunc(slices.Clone(entries), isPathEntry(folder))
	if len(kept) == len(entries) {
		return nil
	}
	return e.sys.SetUserPath(strings.Join(kept, ";"))
}

func couldNotRemove(what string, err error) error {
	return &failure{
		msg:   fmt.Sprintf("could not remove %s: %v", what, err),
		hints: []string{"Run the same command again once that is fixed."},
	}
}
