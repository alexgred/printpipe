package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/alexgred/printpipe/internal/config"
	"github.com/alexgred/printpipe/internal/printer"
	"github.com/alexgred/printpipe/internal/server"
)

// serve is the hidden server command: the agent itself, which start runs in
// the background. --config replaces config.json, --dry-run prints nowhere
// (each job goes to the file, or to the log with "-").
func serve(e env) error {
	path, explicit := e.flags["config"]
	if !explicit {
		dir, err := e.sys.ConfigDir()
		if err != nil {
			return err
		}
		path = config.Path(dir)
	}
	cfg := config.Default()
	switch b, err := e.sys.ReadFile(path); {
	case errors.Is(err, fs.ErrNotExist) && !explicit:
	case err != nil:
		return err
	default:
		if cfg, err = config.Parse(b); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}

	logger := log.New(e.stderr, "", log.LstdFlags)
	p, desc, err := pick(cfg, e.flags["dry-run"], logger)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(cfg.Origins) == 0 {
		return fmt.Errorf(`%s: no "origins": no page could print`, path)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("%s: port %d is not 1 to 65535", path, cfg.Port)
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port))
	l, err := e.sys.Listen(addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           server.Handler(server.Options{Version: Version, PID: os.Getpid(), Origins: cfg.Origins}, p),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		ErrorLog:          logger,
	}
	logger.Printf("printpipe %s on http://%s, printer: %s, origins %v", Version, addr, desc, cfg.Origins)

	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	select {
	case err := <-served:
		return err
	case <-e.ctx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

// pick is the printer to print to, and how the log names it.
func pick(c config.Config, dryRun string, logger *log.Logger) (server.Printer, string, error) {
	switch {
	case dryRun == "-":
		return printer.Log{Logger: logger}, "dry run to the log", nil
	case dryRun != "":
		return printer.File{Path: dryRun}, "dry run to " + dryRun, nil
	case c.Queue != "" && c.Device != "":
		return nil, "", errors.New("set either a queue or a device, not both")
	case c.Queue != "":
		return printer.Queue{Name: c.Queue}, "queue " + strconv.Quote(c.Queue), nil
	case c.Device != "":
		return printer.Device{Path: c.Device}, "device " + c.Device, nil
	default:
		return nil, "", errors.New("no printer: set a queue or a device")
	}
}
