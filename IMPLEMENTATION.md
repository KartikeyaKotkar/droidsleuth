# Implemented milestone: main-process capture and offline replay

## Scope and ownership

Implemented the smallest independently testable pipeline:

```text
ADB subprocess -> complete process snapshot -> identity tracker
              -> durable SQLite observation -> offline text/JSON replay
```

- `internal/adb`: explicit serial selection, authorization-state checks,
  subprocess deadlines, bounded stdout/stderr, cancellation and Linux process
  group cleanup. No host shell interpolation or ADB-server shutdown.
- `internal/capture`: Android SDK/current-user/package-UID probes, batched reads,
  strict snapshot parsing, process identity tracking and capture lifecycle.
- `internal/session`: versioned records, private SQLite files, exclusive creation,
  durable commits and streaming read-only replay.
- `cmd/droidsleuth`: `devices`, `watch`, and `replay`, including JSONL replay.

The research brief in PLAN.md is unchanged. No adbtop code was copied or ported.

## Observation semantics

- Identity is boot ID + UID + PID + raw kernel start ticks. Ticks are identity
  tokens; no assumed tick frequency or conversion to wall time is used.
- Only the exact package-name main process and selected UID are captured.
  Secondary/custom-named/isolated processes are outside this milestone.
- Reads verify the target's current command line and UID, bracketed by matching
  start ticks. A race, permission denial, missing start ticks, malformed output,
  failed command or missing end marker makes the attempt a **gap**, not absence.
- A complete successful process list without the target establishes observed
  absence. PID reuse or replacement produces a different identity.
- Gaps do not close identities or invent lifecycle transitions. The next change
  is marked `after_gap`; identical full identity after reconnection remains the
  same instance. A changed boot starts new identity observations without claiming
  absence in the previous boot.
- Every record has a sequence and host UTC/monotonic request bounds. Successful
  snapshots also retain device epoch seconds and uptime. Replay follows capture
  sequence; no cross-clock alignment or exact death timestamp is claimed.
- `appeared` includes an already-running process first observed at capture start;
  it does not establish that the process started at that instant.
- `no_longer_observed` means the previous instance was not in a later complete
  snapshot. It does not distinguish exit, crash, stop or kill.

## Persistence and resource limits

SQLite is used through `database/sql` and the pinned, CGo-free
`modernc.org/sqlite` driver. Each observation and its transitions are committed
atomically as one row. One sequential collector owns one writer: there are no
queues, concurrent collectors or overlapping polls in this slice.

- One bounded ADB shell invocation per snapshot; three-second command deadline.
- One-second delay after each poll completes; no catch-up burst. Actual cadence
  includes command, persistence and output time.
- Stdout limit 4 MiB, retained stderr limit 8 KiB, observation limit 64 KiB.
- SQLite cache target 4 MiB, database page limit 256 MiB, stop threshold 240 MiB
  to reserve space for completion metadata and the small rollback journal.
- DELETE journaling and FULL synchronous commits; no WAL and no automatic pruning.
- Only normalized target evidence is persisted; unrelated process rows remain
  transient and bounded. Terminal output quotes externally supplied text.
- Duration expiry is `complete`; Ctrl+C/SIGTERM is `interrupted`; persistence or
  presentation errors are `failed`. Abrupt termination leaves `recording`, which
  replay displays as not finalized (active or incomplete).
- Cancellation during a read stores the interrupted attempt as a gap and reaps
  the owned subprocess. Finalization has a separate bounded context.
- Write failures stop capture; failure to finalize is returned to the caller.
  Previously committed observations remain intact.

A database needing hot-journal recovery is reported as an error by read-only
replay. Automatic recovery of a temporary copy is deferred. Do not delete a hot
journal or open the only evidence copy for recovery without preserving it first.

## Real-device validation

Read-only checks on 2026-11-16 established:

| Item | Result |
| --- | --- |
| Device | physical Android phone, USB ADB authorized |
| Android | Android 17, API 37, user/release-keys build |
| Current Android user | 0 |
| Process list | `ps -A -o PID,UID,NAME` supported |
| Boot identity | Kernel boot ID readable |
| Process identity | `/proc/PID/stat` readable for Settings and an ordinary Firefox process |
| Clocks | Device epoch, nanosecond-format date and `/proc/uptime` readable |
| Log-buffer metadata | main/system/crash/events readable; no log bodies retained |
| Exit history | Settings exit-history diagnostic heading readable; parsing/attribution deferred |
| Pipeline smoke check | Four-second Settings capture produced three valid observations of the same full identity |

The smoke capture used no launch, force-stop, installation, crash injection or
device configuration changes. Device evidence is kept under `/tmp`, not in the
repository. Snapshot command durations in this small sample were approximately
0.56–0.65 seconds; this is not a device CPU, battery or overhead benchmark.

## Automated proof

Verified successfully with `go test ./...` and `go test -race ./...` across all
four Go packages. `go vet ./...` also passes. Replay of the real-device recording
was verified with a PATH containing no ADB executable.

Tests exercise:

- Main-process filtering, names with parentheses, malformed/truncated snapshots,
  missing identity files, changed UIDs and invalid clock fields.
- Identity changes in each of boot ID, UID, PID and start ticks; same-PID reuse;
  gaps followed by identical identity or successful absence.
- Fixture-driven fake ADB subprocesses covering appearance, disconnection,
  reconnection, PID reuse, absence, replacement and partial output.
- Cancellation while a fake ADB read blocks, subprocess reaping and persistence
  of the interrupted attempt.
- Subprocess deadlines, stdout overflow and large stderr output. The stdout
  regression checks that `io.Copy` cannot bypass the bounded writer.
- Exact JSON round-trip of committed observations, replay after removing fake
  ADB, and CLI replay with no ADB executable on PATH.
- Exclusive file creation, private permissions, committed-prefix replay after
  an unfinalized close, duplicate-sequence protection, record/database size
  limits, read-only replay and unsupported-schema rejection.

## Limitations and next step

The transport intentionally uses one-shot batched reads in this slice. Persistent
shell framing/recovery, reconnect backoff and source reprobes can follow measured
overhead and compatibility evidence. Current polls retry the same selected serial;
they never select a replacement device. A changed target UID requires a new
recording. Missing boot/start-tick access prevents reliable identity capture.

This milestone has not validated real device restarts, cable removal, API 30
emulators, other OEM builds or low-overhead operation over long recordings.
Polling can miss short-lived process instances. Suspend/clock discontinuity
calibration and multi-source event ordering are deferred.

Next recommended step: validate controlled disappearance/relaunch and USB
disconnection on an explicitly chosen disposable test app, compare the persisted
timeline with independent ADB observations, and measure collection overhead.
Stop at that validation gate before expanding source coverage.

Deferred: crash attribution, ANR detection, logcat collection, exit-history
parsing, metrics, investigation rules, `investigate --last`, secondary-process
tracking, a TUI, dashboards, Perfetto and all later milestones.
