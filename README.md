# printpipe

A small agent for the computer a label printer is plugged into. Web pages in that computer's browser post raw print jobs (such as TSPL) to it at `http://127.0.0.1:8631`, and it hands the bytes to the printer untouched, without a print dialog. It knows nothing of the label layout, so a new layout never needs a new printpipe.

The same binary is the `printpipe` command and the background agent.

> Work in progress: the first release will be v0.2.0. Installing, the background service and most commands are not there yet.

## Commands

```
printpipe help        list the commands
printpipe version     print the version (offline)
```

`printpipe help` lists the commands of the coming release; the ones not there yet say so. Exit codes: 0 success, 1 any failure, 2 wrong usage.

## Configuration

`config.json` in the user's configuration folder (`%APPDATA%\printpipe\` on Windows, `~/Library/Application Support/printpipe/` on macOS, `~/.config/printpipe/` on Linux):

| key | |
|---|---|
| `port` | port on 127.0.0.1, default 8631 |
| `origins` | the pages allowed to print, such as `["https://app.example"]` |
| `queue` | the printer queue: a CUPS queue, or a Windows printer's name |
| `device` | a device instead of a queue, such as `/dev/usb/lp0` on Linux |

## HTTP

The agent listens on `127.0.0.1` only. There is no token: the `Origin` check keeps other sites out.

- `GET /health` → `{"name": "printpipe", "version": "…", "pid": …}`, readable by any page (`Access-Control-Allow-Origin` is the requesting origin).
- `POST /print` with the job as the body (`application/octet-stream`, at most 64 KB) from one of `origins`. The job must print 1 to 10 labels in total, counted over its `PRINT m[,n]` commands as m·n.

## Development

Go only: `make test`, `make lint`, `make fmt`, `make build` (static binaries for Windows, macOS and Linux into `dist/`). `make dev` runs the agent in dry run with `dev.json`: each job lands in `dry-run.tspl`.
