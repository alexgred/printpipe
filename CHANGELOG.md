# Changelog

All notable changes to printpipe. Versions follow [semver](https://semver.org/).

## Unreleased

- One binary for the command and the background agent: `printpipe version`, `printpipe help`; the agent runs as the hidden `printpipe serve`.
- Configuration in `config.json` in the user's configuration folder: `port` (default 8631), `origins`, `queue`, `device`.
- `GET /health` answers `{"name": "printpipe", "version": …, "pid": …}` with CORS for the requesting origin.
- `POST /print` answers every error as JSON `{"code", "message"}` (`printer_not_found`, `printer_ambiguous`, `printer_busy`, `printer_not_responding`, `print_failed`, `origin_not_allowed`, `bad_job`), and every answer, the 403 included, carries CORS for the requesting origin. No `Access-Control-Allow-Private-Network` header.
