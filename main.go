// printpipe runs on the computer a label printer is plugged into: web pages
// in the browser there post raw print jobs to it at 127.0.0.1, and it hands
// the bytes to the printer untouched. The same binary is the command an admin
// types and, as the hidden serve command, the background agent.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexgred/printpipe/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.OS{})
	stop()
	os.Exit(code)
}
