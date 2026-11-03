# printpipe

A small agent for the computer a label printer is plugged into: web pages send it print jobs and it hands them to the printer.

## Printing

**Print agent**:
A program on the computer a label printer is plugged into, through which web pages send labels to the printer. The same program, run as a terminal command, installs, starts, stops and upgrades itself.
_Avoid_: driver, print server

**Label**:
A sticker the label printer prints. Its layout is up to the page that sends it; the print agent knows nothing of it.
_Avoid_: price tag, tag

**Test label**:
A label printed only to check that the print agent's connection to the printer, the roll size and Cyrillic are right; it is built into the print agent and comes from no page.
_Avoid_: sample label, demo label
