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

The initial sampling-overhead concern has been addressed by the optimization
follow-up below. The original validation results are retained as historical
evidence. No additional collector or Milestone 2 feature was added.

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

**Historical decision before optimization: correctness passed, but the performance
gate remained open.** The measured device command cost was
material for an investigation tool whose observations should minimally perturb
the target. No numeric overhead threshold was agreed beforehand; this is an
engineering readiness recommendation, not a claim that a predefined benchmark
limit failed. Profile and reduce the full-device process census and per-row shell
work without weakening identity or absence validation, then repeat the paired
measurement. Also validate replacement during a real coverage gap before
claiming that branch is hardware-proven.

## Sampler optimization follow-up: physical-device measurements

### Scope and result

The production change is confined to `SnapshotCommand` in commit `16122e6`.
The baseline is the production code at `6a1b16f`; its capture implementation is
unchanged from the accepted first milestone. Regression-test checkpoint
`f168deb` precedes the optimization.

**The performance gate is ready for provisional closure for the tested phone and
current main-process scope.** The final paired measurements show 73.6% less
device CPU per snapshot above the empty-shell control, and an estimated 64.0%
reduction per recording second after accounting for the faster cadence. This is
an engineering recommendation based on a substantial measured reduction and
preserved correctness, not a newly invented numeric acceptance threshold.

Every measurement in this section used the real, unrooted Android 17/API 37
phone with eight online CPUs. Earlier emulator exploration was excluded from
these results. Fake ADB was used only for automated regression tests. The
disposable app, fixture source, SDK build procedure and approved mutation
commands are the same as in the original validation gate. The app was removed
afterward; uninstall succeeded and subsequent package/PID queries were empty.
No phone debugging settings were changed.

### Profiling and rejected approaches

The phone reports `printf` as a tracked alias for `/system/bin/printf`, while
`print` is a shell builtin. The original single-main-instance script contains
ten `printf` calls, including its start-tick helpers, in addition to nine other
external utility invocations. The optimized fast path needs only `date`, `ps`
and `sed` as external utilities. These are counts of explicit utility
invocations, not a claim to have traced every fork or kernel syscall.

Component and candidate timings below came from eight randomized rounds on the
physical device. They include shell startup and must not be added together as
an exact decomposition; frequency and background load varied between samples.

| Operation | Mean device user+system CPU seconds | Interpretation |
| --- | ---: | --- |
| Empty shell control | 0.0166 | Measurement startup baseline |
| Original clock reads | 0.0798 | `date` and two `cat` invocations |
| Full `ps` census | 0.0660 | Retained as the absence/discovery source |
| Census plus row-filter loop | 0.1285 | Pipeline and per-row work add cost |
| `pidof` | 0.0421 | Faster positive lookup, insufficient absence evidence |
| UID-constrained `ps -U` | 0.0614 | Little CPU saving; changes what is visible |
| PID-constrained `ps -p` | 0.0490 | Does not discover a replacement/new PID |
| Original command with a fixed known PID list | 0.4561 | Removes enumeration but retains formatting/read overhead; not a complete collector |
| Original full command | 0.5009 | Initial profiling block, distinct from the final A/B block |
| Native row filtering with `grep` | 0.5521 | Rejected: no CPU improvement |
| Validating `awk` filter | 0.4952 | Rejected: much smaller output, negligible CPU improvement and another tool dependency |

`pidof` returns no matches when no readable matching command line is found;
upstream Toybox's `names_to_pid` skips command-line read failures. Its no-match
status therefore does not distinguish absence from incomplete visibility. A
cached-PID `/proc` read or constrained PID query cannot discover a new PID.
UID-only selection could also hide a target UID change that the existing
collector correctly reports as a gap. None was adopted as an absence shortcut.
Source inspection used [Toybox pidof](https://github.com/landley/toybox/blob/master/toys/lsb/pidof.c),
[its enumeration helper](https://github.com/landley/toybox/blob/master/lib/lib.c)
and [ps selection](https://github.com/landley/toybox/blob/master/toys/posix/ps.c);
these mutable upstream references support the limitation analysis, rather than
claiming an exact source match to the installed binary.

### Implemented collection changes and invariants

- Use mksh's raw `print -r --` when available, falling back to `printf` on other
  shells. Process text remains literal, including backslashes and leading
  option characters.
- Read nonempty uptime, boot ID and stat files through shell bulk file
  substitution. If a shell implements redirection-only substitution as empty
  output, use `cat`; a read error or genuinely empty required file still fails
  the snapshot.
- Read the first NUL-delimited command-line argument with the shell builtin,
  falling back to the existing `tr`/`head` sequence if that operation fails or
  is unsupported.
- Compare bracketing start ticks using the existing helper's result variable,
  eliminating two output substitutions. UID validation still reads the real
  UID from `/proc/PID/status` and checks the exact process name.

The full successful `ps -A -o PID,UID,NAME` census is retained on every poll,
including every unrelated row for host-side validation. Empty/failed targeted
probes are never converted to absence. A target listed without complete identity
evidence remains a gap. Identity remains boot ID + UID + PID + start ticks; both
stat reads still bracket name/UID validation. No PID cache, weaker parser, session
schema change, transport change or new dependency was introduced.

### Final paired CPU measurements

After the long captures, twelve randomized rounds compared the actual frozen
baseline and optimized command sources against an empty `sh -c true` control.
Each generated command was passed as one quoted argument to Android's
`/system/bin/time -p sh -c`. The runner required a present target and successful
complete output in every timed snapshot. Only timing values and byte counts
were retained from the full process-list output.

| Measurement | Baseline | Optimized |
| --- | ---: | ---: |
| Mean device CPU seconds including shell startup | 0.616521 | 0.178657 |
| Mean empty-shell control CPU seconds | 0.021210 | 0.021210 |
| Mean CPU seconds above control | **0.595310** | **0.157447** |
| Mean command wall seconds in timed wrapper | 0.609376 | 0.185673 |
| Mean raw stdout bytes per snapshot | 26535 | 26536 |

The paired CPU reduction above control is **73.55%**. The historical 0.409-second
estimate came from a different recording; it is not substituted for this run's
fresh baseline. Conditions visibly changed between profiling blocks, which is
why the final randomized paired block supplies the comparison.

### Longer recording, host resources and lifecycle parity

A 60.70-second idle control preceded 180.84 seconds of baseline recording and
180.80 seconds of optimized recording. Independent ADB references ran at the
same ten-second cadence in each window. They validated a successful full `ps`
view, compared `pidof` with that view, and read the selected process's UID, boot
ID and start ticks. The runner required stable independent identities throughout
each steady window. Snapshot statistics include the startup-verification
snapshot and the successful prefix through each window's endpoint; CPU windows
start after verification. An earlier short profiling attempt with no live fixture was
discarded; the fixture was relaunched before the completed measurements.

| Measurement | Baseline | Optimized |
| --- | ---: | ---: |
| Successful observations through steady endpoint | 106 | 145 |
| Steady-window gaps | 0 | 0 |
| Mean start-to-start interval, seconds | 1.713526 | 1.257877 |
| Median start-to-start interval, seconds | 1.767652 | 1.267159 |
| p95 start-to-start interval, seconds | 1.823027 | 1.303972 |
| Mean observed command duration, seconds | 0.710907 | 0.256386 |
| Host capture/client CPU, percent of one CPU | 0.481 | 0.459 |
| Peak capture RSS, KiB | 13640 | 15720 |
| Database bytes after recording and lifecycle check | 73728 | 94208 |
| Aggregate device busy CPU, percent | 13.85 | 11.37 |

Aggregate idle-control device CPU was 11.98%. The optimized window being below
that control demonstrates uncontrolled background variation; it does not mean
negative overhead. Host CPU includes waited child ADB clients and excludes the
shared ADB server. Peak host RSS remained below 16 MiB in both runs.

Using burst CPU cost and observed mean cadence gives an approximate scheduled
cost of 34.74% versus 12.52% of one CPU, or 4.34% versus 1.56% of eight CPUs.
That is a **64.0% estimated reduction per recording second**, not a directly
isolated whole-device utilization or battery measurement. The default remains
one second of delay after each completed poll; it was not changed to slow down
collection. Faster polling produces about 36% more observations and raises
estimated raw payload throughput from 15.1 to 20.6 KiB/s. The larger recording
and traffic are the explicit tradeoff; per-snapshot output remains unchanged.

Both implementations then underwent the approved disposable-app force-stop and
relaunch sequence. Both produced exactly `appeared`, `no_longer_observed`, and
`appeared`, with successful empty snapshots during absence and new identities
matching independent ADB. No termination cause was inferred. Offline JSON replay
matched all 118 baseline records and 160 optimized records exactly with no ADB
on PATH, and replay left each database unchanged. The optimized recording's one
gap was its final canceled read during SIGINT; the steady window had no gaps.

### Regression proof, limits and closure recommendation

`go test ./...` and `go test -race ./...` pass. Generated-script tests now execute
against a controlled proc tree, exercising native-equivalent raw printing and
forced portable fallbacks. Cases cover successful absence, nonzero census status
despite a valid header, malformed/duplicate unrelated rows, changed UID, missing
or empty identity data, command-line mismatch/arguments, a start-tick change
between reads, literal process text, and multiple main instances. Existing
fake-ADB tests still cover PID reuse, disconnect/reconnect, cancellation,
coverage-gap persistence and offline replay. No product logic outside the
collection command changed.

Physical USB was not cycled again during optimization. Its earlier validation
remains relevant because the transport and tracker are unchanged; reconnection
and `after_gap` behavior also pass the regression suite. Replacement during an
actual disconnected interval remains a hardware-testing limitation, not a newly
claimed result.

This is one Android 17 phone with uncontrolled DVFS, background load and screen
state. The two long windows were sequential; the CPU microbenchmarks randomized
order within each round. No power/thermal benchmark or kernel-level tracing was
performed. Other Android versions/OEM shells were not physically benchmarked;
portable fallbacks preserve behavior but may retain higher overhead. Published
figures deliberately exclude emulator and fake-data results. Raw artifacts stay
private under `/tmp/droidsleuth-opt-real`; no identifying machine-readable
device report was committed.

The measured reduction and correctness parity support provisional closure of
the current main-process performance gate on this device. Residual census/read
cost remains measurable, and future collectors must be assessed against this
new baseline. No Milestone 2 features were implemented or started.
