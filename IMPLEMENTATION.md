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

The disposable-app lifecycle and physical USB cycle have now been validated on
the device below. API 30 emulators, other OEM builds and device reboots remain
unverified. Polling can miss short-lived process instances. Suspend/clock
discontinuity calibration and multi-source event ordering are deferred.

The next step is to reduce and remeasure the sampling overhead documented below
before expanding source coverage. No optimization or new collector was added
during this validation gate.

Deferred: crash attribution, ANR detection, logcat collection, exit-history
parsing, metrics, investigation rules, `investigate --last`, secondary-process
tracking, a TUI, dashboards, Perfetto and all later milestones.

## Real-device validation gate — 2026-11-16

### Environment and authorization

The validated product code is commit `3a4d451`; fixture source is commit
`712e3a5`. The host runs Linux on x86-64 with Go 1.27.3. The device is a physical Android phone running Android 17/API 37, build
`an unbranded Android 17 user build`.
It has eight online CPUs, `CLK_TCK=100`, and 4096-byte pages. USB debugging was
authorized and Android user 0 was selected. Root was not used.

The disposable package `io.github.droidsleuth.validation` was absent before
installation. Its fixture requests no permissions, contains one idle Activity,
and has no services or application data storage. It was built with JDK 21.0.13,
Android SDK platform 36/build-tools 36.0.0, minSdk 30 and targetSdk 36. The APK
SHA-256 is `e2df950464b5ce356258139ec6330f3064b89c9edd76cc5a928d58442b9dd3af`.

The user explicitly approved the following commands before any device mutation:

```sh
adb -s SERIAL install --user 0 /tmp/droidsleuth-validation-gate/disposable.apk
adb -s SERIAL shell am start --user 0 -W -n io.github.droidsleuth.validation/.MainActivity
adb -s SERIAL shell am force-stop --user 0 io.github.droidsleuth.validation
adb -s SERIAL uninstall io.github.droidsleuth.validation
```

Installation used no replacement flag. Only the disposable package was launched
or terminated. The user physically unplugged and reconnected the USB cable after
a separate ready prompt. No USB mode, networking, debugging authorization, power
setting or production application was changed. Cleanup uninstall succeeded;
subsequent package and PID queries returned no matching app.

### Procedure and observations

An idle baseline lasted 60.79 seconds. A single recording then lasted 278.35
seconds, including a 180.45-second steady capture window, approved force-stop
and relaunch, a physical USB cycle, and SIGINT finalization. Independent ADB
references ran every ten seconds during both measurement windows and around
the lifecycle actions. They read the boot ID, `pidof`, `/proc/PID/stat`, UID from
`/proc/PID/status`, `/proc/stat` and `/proc/uptime`. USB presence was independently
polled with `adb devices -l` approximately every two seconds during that test.

- **Initial appearance passed.** Record 1 observed UID 10497, PID 27086 and start
  ticks 106218123 in boot `BOOT_ID`. Independent ADB
  returned the same full identity. `appeared` meant first observed, not a claim
  that process creation occurred at capture start.
- **Controlled disappearance passed.** Independent ADB returned no PID after
  force-stop. Record 112 was a complete empty snapshot and emitted only
  `no_longer_observed`. The recorder did not label a crash, ANR, kill reason or
  force-stop; the experiment's action log supplies that external ground truth.
- **Distinct relaunch passed.** Independent ADB and record 118 agreed on UID
  10497, PID 17339 and start ticks 106280671 in the same boot. Record 118 emitted
  `appeared`. The recording continued through the absence without opening a new
  session.
- **Physical USB recovery passed.** Independent device-list samples bracketed
  approximately 30.04 seconds of disconnection. Records 146–174 were 29 gaps,
  with no lifecycle transitions. Record 175 recovered the unchanged full
  identity and emitted no transition. No fabricated disappearance or restart
  appeared on reconnect.
- **Gap semantics passed for unchanged identity.** A gap did not close the old
  instance. Because there was no identity change on recovery, there was no
  transition requiring `after_gap=true`. A focused unit test additionally checks
  that both transitions receive `after_gap=true` when a replacement is first
  observed after a gap. Replacement while USB is disconnected was not exercised
  on hardware; that positive branch remains a stated hardware limitation.
- **Offline replay passed.** All 183 stored observations (153 snapshots and 30
  gaps) matched JSON replay exactly with `PATH=/nonexistent`. The extra gap was
  the final canceled read during SIGINT, not another USB failure. The database
  SHA-256 was unchanged by replay. It contained 102400 bytes and finalized as
  `interrupted`.

### Cadence and approximate overhead

Continuous successful snapshot start-to-start intervals had a median of 1.607
seconds, mean 1.626 seconds, p95 1.808 seconds and range 1.219–1.847 seconds.
Snapshot command durations had a median of 0.608 seconds, mean 0.627 seconds and
p95 0.807 seconds. These figures exclude gap-spanning intervals. The configured
one-second post-poll delay does not provide one-Hz sampling.

Host capture/client CPU averaged 0.377% of one CPU over the steady window,
including waited child clients but excluding the shared ADB server. Peak capture
RSS was 18108 KiB (17.68 MiB). The low host cost does not establish low device
cost.

Aggregate device busy CPU was 11.26% during the baseline and 17.03% during steady
capture, a 5.77-percentage-point increase. This is one sequential comparison on a
phone with uncontrolled background activity, screen state and DVFS; the full
increase cannot be causally assigned to DroidSleuth.

To isolate approximate command cost, six read-only snapshot/control pairs ran
after recording, using Android's `/system/bin/time -p sh -c ...`. Each snapshot
used the actual `SnapshotCommand` source and the disposable target. Mean device
user+system CPU was 0.4214 seconds per snapshot and 0.0126 seconds for the empty
shell control, leaving approximately 0.4088 CPU seconds per snapshot. At the
observed mean cadence this estimates **25.1% of one CPU**, or **3.14% of eight
CPUs**, excluding other ADB service work. The short burst and control subtraction
make this an approximation, not a battery or thermal benchmark.

Mean snapshot output was 27470 bytes: approximately 16.5 KiB/s at the observed
cadence, excluding transport framing. The timed check discarded unrelated
process-list content and retained only byte counts and timings.

### Changes, evidence and readiness

No product correctness bug was found and no production Go code was changed.
Changes are the disposable APK fixture/build script, a targeted post-gap
replacement unit test, and this report. Raw device/session artifacts remain
private under `/tmp/droidsleuth-validation-gate`; the committed report omits the
ADB serial. The research plan is unchanged.

**Correctness passes on this device; the performance gate remains open. Do not
expand into Milestone 2 collectors yet.** The measured device command cost is
material for an investigation tool whose observations should minimally perturb
the target. No numeric overhead threshold was agreed beforehand; this is an
engineering readiness recommendation, not a claim that a predefined benchmark
limit failed. Profile and reduce the full-device process census and per-row shell
work without weakening identity or absence validation, then repeat the paired
measurement. Also validate replacement during a real coverage gap before
claiming that branch is hardware-proven.
