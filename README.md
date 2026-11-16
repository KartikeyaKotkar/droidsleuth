# DroidSleuth

A Linux-first, local Android process recorder written in Go. This first milestone
records a package's **main process**, preserves observations and coverage gaps in
SQLite, and replays them without ADB or a connected device.

Process identity is **boot ID + UID + PID + kernel start ticks**. Disappearance
does not establish a termination cause. Snapshots are sampled observations, so
short-lived processes and changes between polls can be missed.

## Build and use

Requires Go 1.23+ on Linux. Live capture requires Android platform-tools on PATH,
an authorized Android 11+ device, readable process identity files and supported
`ps` columns. Compatibility has been checked on one Android 17/API 37 phone;
other Android/OEM builds remain unverified.

```sh
go build -o droidsleuth ./cmd/droidsleuth
./droidsleuth devices
./droidsleuth watch --duration 30s --out session.db com.example.app
./droidsleuth replay session.db
./droidsleuth replay --json session.db
```

Flags precede the package or filename. Use `--device SERIAL` for explicit device
selection and `--user ID` to select an Android user. Capture defaults to the
current device user. Without `--out`, recordings go under
`$XDG_STATE_HOME/droidsleuth` (default `~/.local/state/droidsleuth`). Existing files
are never overwritten. Without `--duration`, Ctrl+C or SIGTERM stops capture.

Replay prints every successful snapshot, including those showing absence, and
every failed/incomplete attempt as a gap. JSON output is JSONL: one metadata
object followed by observation records. Schema version 1 is recorded in metadata
and SQLite's `user_version`. Replay is read-only and never calls ADB.

Recordings are private files (`0600`); the default recording directory is `0700`.
Only the selected main-process observations are retained, rather than the full
device process list. Sessions contain package names, device serials and timing
information; treat them as private diagnostic data.

## Verification

```sh
go test ./...
go test -race ./...
```

See [IMPLEMENTATION.md](IMPLEMENTATION.md) for tested functionality, device
validation, limits and deliberate deferrals. [PLAN.md](PLAN.md) remains the
research and roadmap brief. Crash attribution, ANRs and a TUI are not implemented.
