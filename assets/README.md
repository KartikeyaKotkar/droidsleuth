# Terminal demo provenance

[The demo](incident-demo.gif) shows the updated DroidSleuth TUI from commit
`8301274` opening a retained exit-history recording offline. The recording
came from the API 37 Google Play x86-64 emulator release gate and contains the
project's disposable validation app. See the [release-readiness record](../IMPLEMENTATION.md#runtime-evidence-and-measured-cost).

The recording was copied to a private temporary directory and its ADB serial
was redacted before inspection. Four frames were captured from an actual TUI
session in tmux. The rendered demo masks the emulator PID and UID values. The
database and raw captures stay private.

The demo opens the incident index, selects an exit report with **Enter**, opens
its stored evidence references with **e**, and selects the referenced lines
with **Enter**. Selection counters show where you are in the index, timeline,
and evidence references. The index shows host collection time and the
unproven association. The stored evidence shows Android's reported reason.
