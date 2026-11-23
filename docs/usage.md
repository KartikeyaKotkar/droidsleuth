# Usage guide

Start with the [installation and workflow examples](../README.md). The
commands below use an installed `droidsleuth` binary on PATH.

## Choose a device and package

`droidsleuth devices` lists ADB targets and their states. If multiple devices
are attached, pass `--device SERIAL` to `watch` or `collect-history`. Both commands
default to the current Android user. Use `--user ID` to select another installed
copy of the package. Flags precede positional arguments.

## Post-exit collection

`collect-history --out FILE PACKAGE` requires an explicit, unused output path.
It performs a package-scoped read of Android's retained exit reports. The
recording has no live process samples, CPU/RSS context, or logcat stack
excerpts. The Android reason and affected PID remain source claims.

A denied, unsupported, or malformed read returns an error. If the command
printed a saved path, inspect that file for any retained evidence and gaps.
Package removal, UID changes, and retention limits can restrict history.
When the wall time cannot be converted, the raw time is retained and the
occurrence instant remains unknown. The zone sampled at retrieval may differ
from the zone at the incident.

## Live recording

`watch --summary` prints readiness after the first committed successful
snapshot. A successful snapshot can show the process absent. Stop with
Ctrl+C or SIGTERM, or set `--duration`. The summary lists up to five of
the newest indexed incident anchors and commands for the exact recording.
When there are no anchors, use the printed replay command to inspect stored
observations and coverage. A post-finalization summary error includes the
recording path so you can open it directly.

Use a new filename with `--out`. Without it, `watch` creates a recording
under `$XDG_STATE_HOME/droidsleuth`, normally `~/.local/state/droidsleuth`.
Recordings use mode `0600`. Review any recording, JSON export, or terminal
transcript before sharing.

## Offline investigation

```text
droidsleuth investigate --tui FILE
droidsleuth investigate --timeline --incident ID --before 15s --after 30s FILE
droidsleuth investigate --timeline --json --last
droidsleuth replay --json FILE
```

Use an ID listed for your recording. `L` IDs refer to recorded
disappearances and `E` IDs refer to source reports. Incident views show
host collection bounds separately from Android occurrence time. The default
context window is 30 seconds on each side. `--before` and `--after` accept
durations from zero to five minutes. `--last` selects the newest finalized
recording in the managed directory. Use the explicit path for custom files.

| TUI key | Action |
| --- | --- |
| Arrow keys, `j`/`k`, Page Up/Down | Move or page through a view |
| Enter / Esc | Open selection / go back |
| `g` | Open by incident ID, including IDs beyond the index cap |
| `e` / `w` | Inspect evidence references / cycle context windows |
| `r` / `?` | Retry a read / show help |
| `q` / Ctrl+C | Quit |

Use a terminal with at least 40 columns and 10 rows. For redirected output,
use `--timeline`. The TUI verifies evidence references against their stored
rows and displays captured truncation. Reopen the browser if the file changes.

Replay JSON emits a metadata object followed by stored observations.
`collect-history` and plain `watch` write schema 2. `watch --resources` writes
schema 3. Replay and investigation read schemas 1–3. A finalized recording is
required for timeline mode and `--last`.

CPU is shown as a percentage of one logical CPU across a measured interval.
Multithreaded workloads can exceed 100%. Rates stop across snapshot gaps,
identity changes, counter resets, or ambiguous timing. RSS is approximate
and can include shared pages. Either metric can be unavailable independently.

## Troubleshooting

- **ADB missing or unauthorized.** Put Android SDK Platform-Tools on PATH,
  unlock the device, and accept the debugging prompt.
- **Package or snapshot probe failed.** Verify the package is installed for
  the selected user. Check the API level and available process sources.
- **Expected crash or ANR absent.** Inspect recorded gaps with replay.
  Finite buffers and bounded logcat reads can miss evidence. Try post-exit
  history if Android still retains a report.
- **Recording opens in text mode only.** Use a TTY on both input and output
  for the interactive UI. Use timeline JSON for scripts or redirected output.

See the [implementation record](../IMPLEMENTATION.md) for collection bounds
and validation details.

## Validation scope

The v0.2.0 release gate ran post-exit collection on API 30 AOSP and API 37
Google Play x86-64 emulators. The
[release record](../IMPLEMENTATION.md#v020-release-readiness-gate) gives
the tested image fingerprints and measurements.

Post-release validation used the published v0.2.0 binary on one Motorola
Edge 70 Fusion running API 37. A read-only Settings collection matched 16
retained exit reports against independent package-scoped ADB reads.
SIM Toolkit provided a successful empty-history case. A two-minute Settings
recording captured 99 process snapshots with CPU and RSS and no snapshot
gaps. One logcat poll reported incomplete coverage. History and live
recordings passed offline text, JSON, and TUI checks.
