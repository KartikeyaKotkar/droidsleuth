# DroidSleuth

DroidSleuth is a local Android runtime recorder and investigator for Linux.
It captures a package's main process, scoped crash and ANR reports, available
application exit history, and coverage gaps. Investigate the resulting SQLite
recording offline.

Process identity combines boot ID, UID, PID and kernel start ticks. Process
disappearance establishes an observation, with cause unknown. Diagnostic
reports and candidate process associations carry their own uncertainty.

## Build and capture

Requires Go 1.23 or newer. Live capture uses Android platform-tools on PATH
and an authorized Android 11 or newer device with readable process identity
files and supported process-list columns.

```sh
go build -o droidsleuth ./cmd/droidsleuth
./droidsleuth devices
./droidsleuth watch --duration 30s --out session.db com.example.app
```

Flags precede positional arguments. Use `--device SERIAL` for explicit device
selection and `--user ID` to select an Android user. Capture defaults to the
current device user. Without `--out`, recordings go under
`$XDG_STATE_HOME/droidsleuth` (default `~/.local/state/droidsleuth`). Existing files
are never overwritten. Without `--duration`, Ctrl+C or SIGTERM stops capture.

## Investigate offline

```sh
./droidsleuth investigate session.db
./droidsleuth investigate --last
./droidsleuth investigate --timeline session.db
./droidsleuth investigate --timeline --last
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
by the stored observation rows. New recordings use schema 2, and schema 1
remains readable. Reported facts, unknown attribution and incomplete coverage
are visible in investigation output. The timeline has a separate JSON report
format identified by `report_type: "incident_timeline"` and `version: 1`.

Recordings are private files (`0600`), and the default recording directory is
`0700`. Sessions may contain package names, device serials, timing information
and selected diagnostic text. Treat them as private diagnostic data.

## Verification

```sh
go test ./...
go test -race ./...
go vet ./...
```

See [IMPLEMENTATION.md](IMPLEMENTATION.md) for behavior, validation and source
limitations. Historical physical checks used one API 37 phone. Emulator checks
covered API 30, 33, 35, 36 and 37. [PLAN.md](PLAN.md) is the research and roadmap
brief.

## License

Released under the [MIT License](LICENSE).
