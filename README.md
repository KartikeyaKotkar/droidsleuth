# DroidSleuth

DroidSleuth is a local Android runtime recorder and investigator for Linux.
It captures a package's main process, scoped crash and ANR reports, available
application exit history, and coverage gaps. Investigate the resulting SQLite
recording offline.

Process identity combines boot ID, UID, PID and kernel start ticks. Process
disappearance establishes an observation, with cause unknown. Diagnostic
reports and candidate process associations carry their own uncertainty.

## Install on Linux amd64

Download `droidsleuth-v0.2.0-linux-amd64.tar.gz` and `SHA256SUMS` together from the [v0.2.0 release](https://github.com/KartikeyaKotkar/droidsleuth/releases/tag/v0.2.0). For a local release candidate, use the supplied archive and checksum file.

```sh
sha256sum -c SHA256SUMS
tar -xzf droidsleuth-v0.2.0-linux-amd64.tar.gz
install -Dm755 droidsleuth-v0.2.0-linux-amd64/droidsleuth "$HOME/.local/bin/droidsleuth"
```

Add `$HOME/.local/bin` to PATH or invoke the installed binary by its full path.
The archive includes its source revision, toolchain, and dependency notices.

## Build and capture

Requires Go 1.23 or newer. Live capture uses Android platform-tools on PATH
and an authorized Android 11 or newer device with readable process identity
files and supported process-list columns.

```sh
go build -o droidsleuth ./cmd/droidsleuth
./droidsleuth devices
./droidsleuth watch --duration 30s --out session.db com.example.app
./droidsleuth watch --resources --duration 30s --out resources.db com.example.app
```

Flags precede positional arguments. Use `--device SERIAL` for explicit device
selection and `--user ID` to select an Android user. Capture defaults to the
current device user. Without `--out`, recordings go under
`$XDG_STATE_HOME/droidsleuth` (default `~/.local/state/droidsleuth`). Existing files
are never overwritten. Without `--duration`, Ctrl+C or SIGTERM stops capture.

## Reproduce and investigate an incident

1. Build with `go build -o droidsleuth ./cmd/droidsleuth`. Run
   `./droidsleuth devices` and confirm the intended Android device shows
   `device` status. Use `--device SERIAL` if more than one device is attached.
2. Start a recording before reproducing the problem.

   ```sh
   ./droidsleuth watch --summary --resources --out repro.db com.example.app
   ```

3. Wait for **Capture ready**, then reproduce the problem. Readiness means a
   successful process snapshot was committed. The selected app can be absent
   from that snapshot. Press Ctrl+C to finalize, or set a `--duration` in advance.
4. Review the finalized path and up to five newest indexed anchors on stdout.
   Copy the printed TUI or text command for that exact file. Replace its
   `--incident` ID to inspect another listed anchor. If the binary is outside
   PATH, use its original path in place of `droidsleuth`.

   ```sh
   # Example incident ID. Use the ID and absolute path from your summary.
   ./droidsleuth investigate --tui --incident E42.0 /path/to/repro.db
   ./droidsleuth investigate --timeline --incident E42.0 /path/to/repro.db
   ```

The summary suppresses per-record stdout during capture. Report anchors are
source claims with unproven instance association. Disappearance anchors retain
an unknown cause. An empty index means no incident anchors were recorded.
Use the printed replay command to check stored observations and coverage, and
review any index-cap notice. TUI and text investigation work with ADB absent.

If summary generation fails after finalization, the error identifies the saved
file. Use explicit-file investigation to retry. Printed commands use POSIX
shell quoting for ordinary paths and Bash or Zsh quoting for paths containing
control characters or invalid UTF-8.

## Collect exit history after an incident

If the app has already exited, DroidSleuth v0.2.0 can save Android's retained
exit reports without a live recording. Set up Android SDK Platform-Tools and
confirm authorization with `droidsleuth devices`. The package must still be
installed for the selected Android user.

```sh
droidsleuth collect-history --out incident.db com.example.app
droidsleuth investigate --timeline incident.db
droidsleuth investigate --tui incident.db
```

Use a new output path. `--device SERIAL` and `--user ID` select the target
when needed. The command performs one package-scoped history read. The
recording describes what Android reported and when DroidSleuth retrieved it.
It contains no live process samples, CPU/RSS measurements, or logcat stack
excerpts. Android's reported reason and occurrence time can be examined
offline. Raw occurrence time is preserved when the wall time or device zone
cannot be resolved. Android supplies a zone at retrieval, which can differ
from the zone at the incident. Exact process instance association remains
unproven.

An empty successful read means Android returned no matching retained reports.
Reports may expire or arrive late. Package removal, UID changes, and OEM
permissions or output formats can limit availability. If the source is
denied, unsupported, or damaged, the command exits nonzero.
When a recording was created, use the printed path to inspect any retained
reports and coverage gaps.

## Investigate offline

```sh
./droidsleuth investigate session.db
./droidsleuth investigate --last
./droidsleuth investigate --timeline session.db
./droidsleuth investigate --timeline --last
./droidsleuth investigate --tui --last
./droidsleuth investigate --tui --incident E142.0 session.db
# Use an ID from the index. Context defaults to 30 seconds on each side.
./droidsleuth investigate --timeline --incident L142.0 --before 15s session.db
./droidsleuth replay --json session.db
```

Timeline IDs are local to a recording. An `L` ID selects a recorded disappearance.
An `E` ID selects a source report. Each detail view shows collection context in
recording order, including where possible the full process identity, observed
presence and absence, relevant gaps and evidence references. Device timestamps
describe source events and are displayed separately from host collection bounds.
Use `--json` for structured output. `--before` and `--after` each accept zero
through five minutes.

Timeline mode and `--last` require finalized recordings, including interrupted
or failed captures that successfully finalized. `--last` searches timestamped
files in the default managed directory and reports newer unfinalized recordings
it skips. Pass an explicit filename for a custom recording.

Replay is read-only and offline. JSON replay emits one metadata object followed
by the stored observation rows. Plain `watch` writes schema 2. Opt-in `watch --resources` writes schema 3 with
CPU counters and approximate resident memory for the validated main process.
Use this binary to read schema 3. It also reads schemas 1 and 2. Reported facts, unknown attribution and incomplete coverage
are visible in investigation output. The timeline has a separate JSON report
format identified by `report_type: "incident_timeline"` and `version: 1`.

In a selected incident, CPU is shown as a percentage of one logical CPU
over a measured device-uptime interval. Multithreaded workloads can exceed
100%. RSS measures resident memory and may include shared pages. Both metrics
can be unavailable independently. CPU rates stop across snapshot gaps,
process replacement, counter resets and ambiguous timing. Metrics are
observations about the process and do not establish an incident's cause.

Recordings are private files (`0600`), and the default recording directory is
`0700`. Sessions may contain package names, device serials, timing information
and selected diagnostic text. Review the contents before sharing a database,
JSON export, or terminal transcript.

### Troubleshooting

- If `devices` cannot find ADB, install Android SDK Platform-Tools and put its
  `adb` executable on PATH. If a device appears as `unauthorized`, unlock it
  and approve the USB debugging prompt. With multiple targets, pass
  `--device SERIAL` to `watch`.
- If the package probe fails, check that the package is installed for the
  selected Android user. Live capture requires API 30 or later, readable
  identity files, and supported process-list columns. Secondary, custom-named,
  and isolated processes are outside the main-process lifecycle tracker.
- If an expected crash or ANR is missing, check the exact recording with
  `investigate --timeline FILE` and review coverage with `replay FILE`. Android's
  finite log buffers and bounded filtered reads can miss reports. A successful
  read with zero selected observations establishes only what that read returned.
  Android exit history may report the same incident independently when available.

### Interactive incident browser

`investigate --tui (FILE | --last)` opens the offline incident index in a
terminal. Use `--incident ID` to open a particular timeline directly.
`--before` and `--after` set its initial window. Use `--timeline` for
redirected output or `--timeline --json` for scripts. The TUI needs both
terminal input and output and at least 40 columns by 10 rows.

- Arrow keys or `j`/`k` move. Page Up and Page Down page through long views.
- Enter opens the incident, observation, or selected evidence reference.
  Esc goes back and preserves the list selection.
- `g` opens an incident by ID from the index, including IDs beyond the
  4,096-entry index cap.
- `w` cycles 30-second, two-minute, and five-minute symmetric timeline windows.
  `e` lists exact evidence references for the selected observation.
- `r` reloads a timeline or retries a failed detail or evidence read.
  `?` opens keyboard help. `q` and Ctrl+C quit.

Timelines mark the selected incident and coverage gaps by source. Enter on a
snapshot to inspect available CPU/RSS context and the process identity. Enter on
a reported event to see its source timestamp, uncertainty and competing reports.
Evidence shows only the verified stored line range, with terminal controls
escaped and capture truncation marked. The recording stays read-only. A changed
or replaced file requires reopening the browser.

## Verification

```sh
go test ./...
go test -race ./...
go vet ./...
# Linux PTY smoke test with a finalized recording containing a diagnostic report.
python3 scripts/validate-tui.py ./droidsleuth session.db
```

See [IMPLEMENTATION.md](IMPLEMENTATION.md) for behavior, validation and source
limitations. Historical physical checks used one API 37 phone. Emulator checks
covered API 30, 33, 35, 36 and 37. The v0.2.0 post-exit workflow was verified
on API 30 AOSP and API 37 Google Play x86-64 emulators. [PLAN.md](PLAN.md)
is the research and roadmap brief.

## License

Released under the [MIT License](LICENSE).
