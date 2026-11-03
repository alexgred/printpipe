# Changelog

All notable changes to printpipe. Versions follow [semver](https://semver.org/).

## Unreleased

- One binary for the command and the background agent: `printpipe version`, `printpipe help`; the agent runs as the hidden `printpipe serve`.
- Configuration in `config.json` in the user's configuration folder: `port` (default 8631), `origins`, `queue`, `device`.
- `GET /health` answers `{"name": "printpipe", "version": …, "pid": …}` with CORS for the requesting origin.
- `POST /print` answers every error as JSON `{"code", "message"}` (`printer_not_found`, `printer_ambiguous`, `printer_busy`, `printer_not_responding`, `print_failed`, `origin_not_allowed`, `bad_job`), and every answer, the 403 included, carries CORS for the requesting origin. No `Access-Control-Allow-Private-Network` header.
- A built-in list of known printer models, the Xprinter XP-365B (USB `1FC9:2016`) first; a model not in it is never matched.
- `printpipe test-print [--size W,H]` prints the built-in test label through the running agent: TSPL in CP1251 for the model's roll (XP-365B: 58×40 mm) with one Cyrillic line in font 3.
- Windows prints straight to the USB printer (the usbprint device interface): no admin rights, no driver, no queue. `device: "usb:VID:PID[:serial]"` is found again on every job; several matches are `printer_ambiguous`, none `printer_not_found`, another program holding it `printer_busy`, a write not done in about 10 s `printer_not_responding`. An existing Windows queue is used only when given as `queue`.
