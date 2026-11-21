# DroidSleuth implementation record

## Current scope

Milestone 1 correctness and performance gates are accepted. Milestone 2 adds
bounded diagnostic evidence and offline investigation; its implementation,
validation and limitations are recorded in the final sections below. The
compatibility/reliability gate has completed with a scoped software/emulator
pass; physical/OEM breadth remains open. Earlier
Milestone 1 sections retain their historical scope and measurements. PLAN.md
remains the research and roadmap document.

## Milestone 1: main-process capture and offline replay

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

## Milestone 2 implementation checkpoint

The user accepted both Milestone 1 gates and authorized evidence-backed crash
and ANR investigation. The optimized snapshot command is preserved. The research
plan remains unchanged.

Implementation sequence:

1. Version new recordings as schema 2; retain read-only schema 1 replay.
2. Add bounded, cancellable filtered logcat tail and scoped exit-history polls.
   Keep one database writer and independent diagnostic coverage gaps.
3. Reconstruct source-reported incidents offline, with evidence references,
   conservative candidate instances, uncertainty and conflicting exit reasons.
4. Validate deterministic fixtures, fake ADB, cancellation and storage bounds;
   validate only the disposable application on hardware after command approval.
5. Measure incremental device and host costs, document limitations, then run
   the full test, race and vet checks before the milestone decision.

Current work is in progress; this checkpoint does not declare Milestone 2
complete. No real-device application mutation has occurred for this milestone.

## Milestone 2: bounded incident evidence and offline investigation

### Implemented behavior

New `watch` recordings enable diagnostic collection by default. The original
optimized `SnapshotCommand`, strict snapshot decoder and identity tracker are
unchanged. One snapshot loop and two independent diagnostic workers feed one
SQLite writer. Only snapshot failures affect lifecycle `after_gap` state;
logcat/exit-history gaps cannot fabricate disappearance or restart.

Logcat uses the main, crash, system and events buffers with native tag filters
for `AndroidRuntime`, `DEBUG`, `am_crash` and `am_anr`. Initial access is a
512-record tail; subsequent reads overlap the last decoded realtime timestamp
by one microsecond. Reads occur three seconds after the preceding work ends.
This intentionally uses bounded cancellable dump polls rather than a persistent
stream. Missing overlap, initial-tail saturation, command/output-limit failure,
malformed records, block overflow and source resets produce diagnostic gaps.
Boot ID is checked before and after log reads. No buffers are cleared, Android
settings changed or privileged trace files read. An empty log read never proves
that a crash or ANR did not occur.

Exit history is polled through package-scoped `dumpsys activity exit-info` at
startup and every fifteen seconds after work. Supported empty history is
separate from permission denial, unsupported output and malformed selected
entries. Android's reported reason number and label are retained, including
unknown/signaled/user-requested reasons; SIGKILL is not reclassified as LMK.
Exit-history occurrence boot ID is deliberately unknown because history may
survive reboot. Device-local timestamps are converted using the reported IANA
timezone; unknown zones, invalid timestamps and ambiguous DST folds retain raw
wall time with unknown occurrence time. The device timezone is assumed stable
within a poll; cross-clock calibration and detecting a timezone change during
the read remain limitations.

Java/Kotlin fatal reports require `FATAL EXCEPTION`, an explicit selected process
name, an agreeing affected/header PID and the selected UID. Native reports
require an explicit selected process name, affected UID/PID and a signal marker;
process metadata or a nonfatal debugger dump alone is insufficient. Structured
ANR/crash events require root/system emitter identity, selected Android user and
explicit process name. Exact package names and conventional `package:process`
names are supported; arbitrary custom names, isolated UIDs and shared-UID
ownership beyond those explicit names remain unsupported. Diagnostic secondary
process reports can be retained, but only the main process receives kernel
identity snapshots in this milestone.

### Persistence, limits and replay

New recordings use schema 2 (`PRAGMA user_version=2` and matching metadata).
Schema 1 remains readable offline without migration; newer/unknown schemas are
rejected. Evidence rows atomically contain selected source excerpts, normalized
reported events and one-based line references. Global observation sequence plus
line range identifies evidence; host collection bounds and source occurrence
time/precision are separate. Successful bounded source reads, including reads
with zero selected evidence, are persisted as source-status observations.
Start/end coverage limits and interrupted/delayed final reads are explicit gaps.

Existing 4 MiB ADB stdout, 8 KiB stderr, three-second per-command deadline,
64 KiB observation, 240 MiB stop threshold and 256 MiB database limit remain.
Each excerpt is limited to 8 KiB, Java/native blocks to 128 lines, active block
groups to 16, source observations to 64 per poll and the writer queue to 16.
Overflow produces a gap instead of silently promising completeness. The two
workers use backpressure and cancellation; there are no unbounded goroutine or
message queues. Source deduplication retains 512 fingerprints per worker;
exit-history slot numbers do not define identity. Changed source text or reasons
remain observations, and bounded deduplication can retain old retransmissions
after cache eviction. Offline analysis deduplicates normalized reports while
preserving up to sixteen evidence references per finding.

Unrelated logcat text is only transient in the bounded ADB response. It is never
persisted as raw source text or error diagnostics. Source text is retained only
after explicit scope checks; malformed contradictory process headers are dropped.
Device strings and excerpts are quoted in text output.

`replay` includes all schema-2 observations and still requires no ADB executable
or connected device. `investigate [--json] FILE` makes two bounded read-only
passes and reports source claims, bounded source excerpts, evidence references,
candidate identities, collection gaps and uncertainty. Its JSON report has
independent format version 1. Finding order follows committed observation order;
identity candidates are sorted deterministically. Limits are 4096 observed
instances/findings, sixteen candidates/references per finding and 256 displayed
gaps, with an explicit incompleteness limitation when reached.

PID, name, UID/user, source boot ID when available and source timestamp can
suggest an observed full identity. Even one candidate remains **unproven**:
these diagnostic sources do not supply kernel start ticks. Association only
suggests instances observed within the reported second-resolution time span;
reports outside that span remain unbound rather than matched to the nearest
PID. Missing snapshots, PID reuse, delayed records and clock discontinuities
remain explicit uncertainty. Exit reports before recording may be historical.
Different reported exit reasons for the same UID/PID/name/occurrence timestamp
are retained as conflicting observations; no cause wins by source priority.
ANR followed by crash is not automatically a contradiction. Disappearance alone
is always unexplained, regardless of nearby reports.

### Validation and hardware compatibility

The existing real unrooted Android 17/API 37 phone, user 0, eight CPUs and
`CLK_TCK=100` was used. Host: Linux x86-64, Go 1.27.3, ADB 37.0.1; fixture built
with JDK 21, SDK 36 and build-tools 36.0.0. No emulator measurements are included.
The user approved the exact disposable-app installation, idle/Java/native
launches, foreground ANR broadcast, force-stop and uninstall commands before
any mutation. Installation used no replacement flag and the package was absent.
The fixture requests no permissions and stores no user data. A three-second
delayed Java exception and SIGSEGV action, plus a 120-second blocking test-only
receiver, affect only this disposable package.

Independent scoped ADB logcat and exit-history commands confirmed Java exception
reason 4, native crash reason 5/status 11 and broadcast ANR reason 6. Captures
retained a scoped Java fatal event, native crash indicators and an explicit
`am_anr`, with matching affected PIDs/UIDs/timestamps in the independent source
observations. The corrected 110-second recording contains both native and ANR
log events plus exit-history reports. Controlled force-stops remained reported
reason 10, and process disappearance alone retained unknown cause.

Hardware revealed an optional `ppid` field in the native process header; parsing
was corrected and a sanitized four-line physical-output fixture now tests that
shape, including the explicit signal requirement. This phone's `am_crash`
record layout disagrees with its advertised event-tag order (including a native
record carrying the system emitter PID). It is rejected with a gap rather than
heuristically reordered or trusted for process identity. Java/native diagnostic
blocks and independent exit history supply the usable reports. ANR event order
was supported. Native/Java mode launches that only delivered an Intent to an
existing Activity were not counted as crashes; tests used a fresh fixture launch.

An early bounded-tail-only implementation lost overlap in busy buffers. It was
replaced with the overlapping timestamp cursor. Cross-buffer silent loss can
still occur despite one surviving overlap marker; overlap is a continuity check,
not proof that every buffer retained every record. A malformed retained record
may produce repeated source gaps while it remains in the overlap. Finite Android
rings, missing permissions, OEM formats and polling delays mean some incidents
will be absent or only partially supported. The investigator exposes this and
never treats missing evidence as a negative diagnosis.

Automated validation covers Java and native reports, nonfatal native dumps,
ANRs, unrelated processes/users, PID reuse, missing/unsupported exit history,
malformed/oversized/truncated output, conflicting process blocks, DST ambiguity,
contradictory exit reasons, deduplication, tail rollover, ADB failure/recovery,
subprocess cancellation, schema-1 compatibility, evidence-reference validation,
lifecycle isolation and exact schema-2 offline replay. Offline investigation is
repeated to check determinism and leaves source database bytes unchanged.

No logcat-to-death cause inference, TUI, dashboard, network monitor, Perfetto,
companion service, privileged tombstone retrieval or AI diagnosis was added.

### Incremental collection cost on the physical device

Four 60-second windows ran in baseline/candidate/candidate/baseline order using
the accepted optimized Milestone 1 binary and the new diagnostic collector. A
further 60-second baseline/candidate pair used the build with native-signal and
DST checks. Subsequent malformed-input/metadata-bound guards do not change the
successful collection commands or polling schedule. The disposable app was idle
and its identity stable; no crashes or ANRs were induced during cost windows.
Host measurements include capture and its child ADB clients, excluding the
shared ADB server. RSS is the peak sampled sum across that process tree, every
100 ms; very short child peaks can be missed. CPU includes waited child CPU.

The final pair measured:

| Measurement | Milestone 1 | Milestone 2 |
| --- | ---: | ---: |
| Successful snapshots | 48 | 48 |
| Mean snapshot start interval, seconds | 1.253280 | 1.256706 |
| Snapshot coverage gaps | 0 | 0 |
| Host CPU, percent of one core | 0.499 | 0.932 |
| Peak sampled process-tree RSS, KiB | 15632 | 19796 |
| Final database bytes | 36864 | 53248 |
| Aggregate device busy CPU, percent | 10.906 | 10.979 |

Milestone 2 added 23 source-status records, seven retained historical exit
reports and four diagnostic boundary gaps (two startup, two shutdown). There
were no ongoing source failures in this idle cost window. Host CPU increased by
about 0.433 percentage points of one core; sampled peak RSS increased by
4164 KiB, approximately 4.07 MiB. Mean snapshot interval changed by 0.27% in this
pair. The initial four windows had baseline mean intervals 1.217/1.251 seconds
and candidate intervals 1.210/1.230 seconds; candidate host CPU was
1.024/0.995%, versus 0.580/0.502% baseline. These runs support a modest host
increment without slowing the snapshot schedule materially on this phone.

Whole-device busy CPU varied from 10.9–21.3% in baseline windows and
11.0–34.4% in candidate windows. Background load, DVFS, screen state and log
production were uncontrolled. Neither the final pair's small difference nor the
earlier large difference establishes isolated collector CPU consumption.

Eight randomized rounds additionally timed each actual device operation with
`/system/bin/time -v sh -c ...`, retaining only timings, RSS and stdout byte
counts. No unrelated log bodies were saved. Results:

| Operation | Mean device CPU seconds | Mean wall seconds | Maximum command RSS, KiB | Mean stdout bytes |
| --- | ---: | ---: | ---: | ---: |
| Empty shell control | 0.023618 | 0.025528 | 4680 | 0 |
| Boot-ID read | 0.035736 | 0.037546 | 5160 | 37 |
| Timezone read | 0.031726 | 0.033865 | 4404 | 14 |
| Initial logcat tail | 0.054626 | 0.092231 | 5056 | 0 |
| Timestamp-cursor logcat read | 0.050743 | 0.076239 | 5056 | 0 |
| Scoped exit history | 0.041021 | 0.043126 | 5248 | 3646 |

A steady logcat cycle performs two boot-ID reads plus logcat: approximately
0.122215 device CPU-seconds including timed shell startup, or 0.051361 above
three matched empty-shell controls. An exit-history cycle performs timezone
plus dumpsys: approximately 0.072748 seconds including startup, or 0.025511
above two controls. Using measured command wall times plus the configured
post-work delays estimates approximately **4.4% of one core including those
timed shell costs**, or **1.8% above the matched controls**; on eight CPUs these
are roughly 0.55% and 0.23% of aggregate capacity. These are estimates, not an
isolated total or battery benchmark. The timing wrapper adds its own shell
context, ADB transport time lengthens real intervals, and binder work in
system_server/logd may not be charged to the timed client. The above-control
number intentionally excludes shell costs that still occur during collection.

Peak measured command RSS was about 5.1 MiB. This measures the timed command's
maximum resident set, not simultaneous workers plus Android system services or
proportional memory. Microbench logcat output was empty during idle measurements;
heavy crash stacks, high selected-event rates and saturated buffers were not
resource-benchmarked. The fixture's exit history was small. Bounds and explicit
loss reporting apply under pressure, but this is not a worst-case resource proof.

### Final checks and milestone decision

`go test ./...`, `go test -race ./...` and `go vet ./...` pass, including the
fake-ADB end-to-end evidence pipeline, failure/recovery and cancellation tests.
New metadata and observation reads are bounded to 64 KiB. No dependency was
added and no adbtop code was copied. The optimized snapshot source has no diff
against the accepted Milestone 1 implementation.

Offline verification with `PATH=/nonexistent` compared SQLite payloads with
JSON replay for all 147 initial incident records, 165 corrected validation
records, 48 final baseline records and 82 final candidate records. All matched
exactly. Repeated investigation JSON was byte-identical, and database SHA-256
hashes remained unchanged. These hardware checks supplement the deterministic
fixtures; no fake or emulator result is presented as hardware evidence.

Implementation checkpoints are `abc5e72` (initial bounded evidence pipeline)
and `9cf1931` (hardware parser/coverage fixes and regression tests). Final input
bounds and validation results follow in the closing commit. Capture integration
retains monotonic elapsed bounds before converting source timestamps to UTC;
snapshot elapsed bounds remain those computed by the snapshot loop. Raw recordings,
identifying references and measurement JSON remain private under
`/tmp/droidsleuth-m2-real`; the committed native fixture removes actual times,
UIDs, PIDs, boot identifiers, addresses and build information. Cleanup uninstall
succeeded; subsequent package and PID queries were empty. No physical USB cycle
or device reboot was repeated in this milestone. Source failure/recovery was
validated with fake ADB; Milestone 1's prior USB/snapshot result remains unchanged.

**Milestone 2 is complete for this initial evidence-backed investigation scope.**
It is not a universal crash detector or definitive cause classifier. Missing
logs/history, delayed or revised reports, unsupported OEM layouts, custom
process names, historical boot ambiguity and source clock changes can leave
incidents unresolved. Recommended next step is broader Android/OEM and
high-volume/rollover validation, with special attention to event-field layouts
and long recordings, before expanding attribution or beginning another product
milestone. No later milestone work has started.

## Milestone 2 compatibility and reliability gate (in progress)

The validation plan was reviewed and execution authorized. This gate expands
validation only; it does not begin Milestone 3. The accepted snapshot collector,
identity tracker, schema-1 replay and schema-2 recording contracts are protected.

Acceptance: deterministic evidence and explicit uncertainty; diagnostic gaps
must not change lifecycle transitions; observable loss must remain a gap; bounded
memory, queues and storage; cancellation reaps owned subprocesses; committed
prefixes remain replayable; all required tests/race/vet pass. Hardware, emulator
and synthetic results are recorded separately. Each demonstrated defect must
first have a reproducer and then a focused regression test.

Resource policy: reuse the installed API 37 image, add at most one older image
at a time if needed, and remove only task-created temporary images/AVDs after
validation. Temporary recordings are processed sequentially with a 512 MiB
working budget; no installed user AVD or existing SDK image will be removed.
API 30 and API 37 are prioritized for executable coverage; API 29/33/35/36 remain
explicitly untested unless their images are actually exercised. Synthetic format
variants never establish OEM or Android-version compatibility.

Planned proof: parser/source limit boundaries, high-volume relevant/unrelated
traffic, repeated/missing overlap, rollover, duplicates past cache eviction,
delayed/split reports, clock ambiguity, source interruption/recovery, lifecycle
isolation, real 240/256 MiB SQLite boundaries, bounded offline analysis, a two-hour
fake-ADB soak, and disposable emulator captures. Physical measurements require
a connected authorized phone plus approval of exact fixture commands. Historical
physical results above are not relabeled as new validation.

### Demonstrated defects and first verified checkpoint

Focused tests reproduced seven failures before fixes:

- A native abort message containing `uid: ...` could override the actual UID
  field and retain another user's report; native UID parsing now requires the
  standalone field, and contradictory actual UID fields reject the block.
- Invalid isolated-UID notation could spill into a different Android user;
  isolated application IDs now remain within their 99000–99999 range.
- A previous overlap marker embedded inside another record falsely established
  continuity; overlap now requires the entire original line, preserving its
  leading padding.
- Candidate association included one extra full second beyond the last observed
  timestamp's precision; evidence outside the actual observed second stays unbound.
- Oversized normalized fields could exceed SQLite's record limit and terminate
  capture; the diagnostic producer now rejects evidence over a 60 KiB encoded
  budget, reserves space for the record envelope and emits a source gap instead.
- Unknown UID formats could appear to be successful empty diagnostic reads;
  relevant records with unknown emitter UID now explicitly mark incomplete coverage.

These changes retain evidence requirements, schema 2 and the accepted snapshot
collector/tracker. Unit, fake-ADB, lifecycle-isolation, race and vet checks pass
at this checkpoint. Source-only malformed/oversized failures leave exactly the
same lifecycle transition sequence, including genuine snapshot-gap `after_gap`
flags, as the same snapshots without diagnostic collectors.

The opt-in real SQLite-size tests created and removed actual large databases:
the capture stop occurred after 4062 committed observations at 251662336 bytes;
the SQLite page limit rejected insertion after 4332 committed records at
268398592 bytes (65527 pages, configured maximum 65536). Finalization and full
read-only prefix replay succeeded. The test checks SQLite error code 13 rather
than assuming a driver-specific error string. These are host-storage tests,
not Android hardware results.

### Runtime matrix and follow-up parser proof (execution continues)

API 30 AOSP x86_64 completed a real thirty-minute capture through ordinary shell
UID 2000: 2539 records, 1735 snapshots, Java fatal/native crash/explicit ANR
reports, four scoped exit reports and no stored injected unrelated log messages.
All recorded observations replayed exactly with no ADB on PATH; repeated offline
investigation was deterministic and database hashes remained unchanged. The
recording finalized as complete. Its task-created AVD and downloaded image were
removed before proceeding to API 33.

API 37 Google Play x86_64 is undergoing the two-hour real Android capture,
including 30000 unrelated shell-log writes and a deliberate emulator stop/reboot
at the same serial. Snapshot and diagnostic failures became gaps; resumed
snapshots observe the new boot. This is an emulator reboot test, not a physical
USB test. Both API 30 and API 37 retain Java/native/ANR source reports while
rejecting the observed nonstandard `am_crash` field ordering. The ANR did not
itself establish death; a subsequent approved force-stop could produce reported
user-requested exit reason 10 instead of reason 6. Source claims are not promoted
to a single termination cause.

Sanitized actual emulator snippets and their provenance are now in
`testdata/compatibility`. These fixtures retain the relevant syntax, not the
original timestamps or identifiers. They are separate from the historical
physical-phone native snippet. Numeric/source availability contract tests for
API 29/30/33/35/36/37 remain synthetic unless the corresponding runtime row is
actually executed.

Additional failing-then-passing regressions demonstrated that quoted native
process/signal markers must not be treated as headers; malformed UID fields
cannot be repaired by a later field; invalid UTF-8 must not be silently rewritten
by JSON persistence; affected PIDs must fit Android's signed integer domain;
source fractional-second precision must not be overstated; interleaved Java
fatal threads must use distinct PID/TID assembly keys; and truncated event
vectors must remain gaps. No evidence requirement was relaxed. The normalizer,
not the accepted snapshot collector/tracker or recording schema, owns these fixes.

A fifteen-minute initial parser fuzz run completed 7540440 executions without a
failure. A further run and post-fix soak supplement the focused tests; fuzzing is
software validation only and establishes no Android/OEM hardware compatibility.
The two-hour fake-ADB soak is independent from the two-hour Android emulator
recording and its results will be identified separately.

The reproducible disposable runtime harness is
`scripts/validate-android-emulator.py`. It refuses existing AVD names/serials and
existing fixture installations, checks SDK and ordinary shell UID, builds the
current Go code, runs the approved fixture scenarios and volume workload, waits
for the requested capture duration, replays offline, and removes its task-owned
AVD/image. It keeps a downloaded image if another AVD references it. Review and
approve its device-mutating commands before execution. No root or existing user
AVD is used. The original SDK API 37 image is reused and retained.

### Gate overhead methodology and interim measurements

The baseline is accepted Milestone 2 commit `52ee5b4`, built from a frozen
archive with the same Go compiler and pinned dependencies. Four sixty-second
baseline/candidate/candidate/baseline windows ran per emulator after the noise
workload finished. The endurance observer remained active in both arms. Two
emulators and other validation work shared the host, so these are approximate
paired measurements rather than isolated physical-device CPU measurements.

| Runtime | Baseline host CPU (% one core) | Candidate host CPU | Baseline snapshot interval (s) | Candidate interval (s) |
| --- | ---: | ---: | ---: | ---: |
| API 30 AOSP | 0.981 | 0.980 | 1.036923 | 1.037340 |
| API 37 Google Play | 1.019 | 1.002 | 1.046602 | 1.045390 |

All these windows had 58 successful snapshots, zero snapshot gaps and 57344-byte
recordings. The small CPU differences are within run variation and are not
claimed as an optimization. Guest busy CPU was approximately 4.4–4.7% on API 30
and 7.4–8.2% on API 37; these are virtual guest utilization figures and cannot
substitute for phone CPU/battery overhead. The collection commands and poll
schedule are unchanged; changes are validation and conservative attribution.

The initial RSS traversal sampled only the main Go thread's child list, which
can miss subprocesses launched by other Go OS threads. Those RSS values are not
used as a full-tree bound. A corrected API 37 pair inspected every OS thread's
child list at 100 ms intervals: baseline/candidate peak sampled tree RSS was
29696/23736 KiB, host CPU 1.0590/1.0596% of one core, and mean snapshot interval
1.047996/1.049229 seconds, with 58 successful snapshots and zero gaps in each
window. Different child overlap and sampling phase prevent treating the RSS
difference as a memory saving. Independent ten-second all-thread sampling of
the longer captures supplements this pair; it can still miss short peaks.

The parser benchmark mixes selected ANR records with equally many unrelated
runtime records, at 100/1000/10000 selected records per batch. These are batch
sizes, not asserted Android emission rates. Three repetitions produced these
median results:

| Selected records/batch | Baseline time | Candidate time | Baseline bytes/op | Candidate bytes/op |
| --- | ---: | ---: | ---: | ---: |
| 100 | 0.553 ms | 0.555 ms | 111436 | 111444 |
| 1000 | 5.167 ms | 5.635 ms | 1067301 | 1067727 |
| 10000 | 55.824 ms | 59.803 ms | 12092680 | 12099794 |

Strict UID/TID/integer/encoding/framing/precision checks add approximately 7–9%
parser time in the larger batches on this host; allocations are essentially
unchanged. This is a correctness tradeoff, not a device CPU estimate. Real shell
log injection on API 30 wrote 30000 records in 147.9 seconds (about 203 writes/s)
and API 37 in 203.8 seconds (about 147 writes/s). The shell command forks a logger
per write, so these rates do not represent logd throughput limits. Synthetic
10000-record bursts separately exercise the collector's 64-observation limit
and queue cancellation. No lossless high-volume collection claim is made.

## Compatibility/reliability gate result — 2026-11-18

**Scoped pass:** software reliability checks and real Android emulator validation
completed for API 30, 33, 35, 36 and 37. This is not a new physical-device or
multi-OEM certification. No Milestone 3 feature was implemented.

### Reproducible compatibility matrix

All runtime rows below used fresh task-owned x86_64 AVDs, Android user 0,
ordinary shell UID 2000, two virtual CPU cores and software graphics. API 37
uses the existing Google Play image; API 30/33/35/36 use AOSP default images.
The emulator executable was 37.2.13 with KVM on Linux x86-64. The Go compiler
was 1.27.3 on the same Ryzen 7 4800H host. Requested guest RAM was 2048 MiB;
the API 37 emulator raised its minimum to 4096 MiB. Boot defaults enlarged
log buffers; actual buffer metadata is retained in private reference artifacts.
These emulator policies do not establish production OEM permission behavior.

| API | Evidence class | Runtime duration | Records | Successful snapshots | Java/native/ANR reports | Exit history |
| --- | --- | ---: | ---: | ---: | --- | --- |
| 29 | synthetic SDK capability contract only | not booted | — | — | not runtime-tested | unsupported minimum verified by fixture |
| 30 | Android 11 AOSP emulator | 30 min | 2539 | 1735 | all three observed | reasons 4, 5, 10 observed |
| 33 | Android 13 AOSP emulator | 30 min | 2454 | 1721 | all three observed | reasons 4, 5, 10 observed |
| 35 | Android 15 AOSP emulator | 30 min | 2455 | 1723 | all three observed | reasons 4, 5, 10 observed |
| 36 | Android 16 AOSP emulator | 30 min | 2450 | 1719 | all three observed | reasons 4, 5, 10 observed |
| 37 | Android 17 Google Play emulator | 2 h, plus 15 min final-parser repeat | 9877 + 1242 | 6737 + 860 | all three observed in both recordings | reasons 4, 5, 10 observed |
| 37 physical phone | historical Milestone 2 evidence only | no new run | — | — | historical results above | no new measurement |
| Other OEM firmware/user profiles | untested runtime | — | — | — | synthetic rejection cases only | no availability claim |

The API 30 package was revision 11 (embedded source.properties revision 10);
API 33/35/36 AOSP images were revision 2. The installed API 37 package metadata
was revision 8 (embedded source.properties revision 7). Per-run manifests retain
image properties, shell identity, binary hashes, source hashes and action phases.
API 33's package contained unresolved extension-level template strings; no
extension-level capability is inferred from those placeholders. SDK level and
source capabilities were checked against the running Android instance.

| Format/source case | Proof | Result |
| --- | --- | --- |
| Epoch/threadtime/UID Java fatal records | actual snippets from all five emulators; deterministic fixtures | supported with explicit process/PID/UID checks |
| Native standalone UID/process/signal records, optional `ppid` | actual emulator snippets and historical physical snippet | supported; quoted message text cannot act as headers |
| Explicit `am_anr` vector | observed on all five emulators | supported with trusted emitter and selected user/process |
| Nonstandard `am_crash` PID/user ordering | independently observed/reconstructed examples and rejection fixtures | remains rejected with gaps; no field swapping or guessed affected PID |
| Missing/unknown UID, malformed integers or event framing | deterministic hostile fixtures | rejected/incomplete coverage, not a PID-only fallback |
| Unknown timezone or ambiguous DST wall time | deterministic fixtures | raw exit time retained; occurrence instant unknown |
| Shared/custom-named/isolated processes and additional OEM layouts | limited synthetic scope tests; no new runtime certification | original scope limits remain |

No synthetic test is presented as an API runtime, OEM or hardware observation.
The small fixtures in `testdata/compatibility` derive from the actual emulator
records, with identifiers and irrelevant detail normalized as documented there.
The older physical native fixture remains explicitly historical.

### Endurance, interruption, volume and resource results

The real API 37 recording finalized after its two-hour deadline. Read-only
verification found one failed snapshot followed by the identical complete
identity and no transition after the approved ADB reconnect. A second interruption
contained 154 failed snapshot attempts and recovered in a different boot with
no fabricated old-boot disappearance. Diagnostic gaps had no lifecycle
transitions. This demonstrates emulator ADB/reboot behavior, not physical USB
replacement or recovery on a phone.

Across continuous successful same-boot API 37 intervals, excluding gap-spanning
intervals, mean cadence was 1.045424 seconds, median 1.044638 and p95 1.054105.
Including the deliberate interruption gives a mean of 1.068843 seconds and is
not a steady-cadence estimate. The source gaps also include rejected retained
records; they are not all transport outages. API 30/33/35/36 had no snapshot
gaps in their completed thirty-minute runs. Their logcat gap counts were
82/14/14/14 respectively, with two exit-history boundary gaps each. Unsupported
retained records may cause repeated source gaps until the cursor advances.

Each emulator executed three approved 10000-message unrelated-log loops, for
150000 attempted writes across the five images. API 33/35/36 needed approximately
188.8/187.4/202.6 seconds for their 30000 writes. These messages exercised finite
buffers and cursor movement without becoming stored source evidence. All complete
recordings were scanned for the injected marker and none contained it. Forced
rollover/missing markers, duplicates including cache churn, delayed evidence,
queue backpressure and malformed/truncated inputs are additionally covered by
deterministic tests. Cross-buffer silent loss remains possible; these tests do
not establish lossless logcat capture or an exact Android dropped-record count.

The independent two-hour fake-ADB soak completed in 7200.58 seconds: 19567
committed observations, a 10080256-byte database, 6950 successful snapshots,
23 deliberate snapshot failures and thousands of injected source failures,
overwhelming bursts and repeated records. Its live observation hash matched
full offline replay. At thirty-second samples, Go heap was 1053656–13159016
bytes and goroutine counts were 7 or 10, with no observed sustained growth.
A fifteen-minute post-fix repetition completed with 2471 observations and a
1286144-byte database; sampled heap was 992688–12452360 bytes and goroutines
4 or 7. The long run began after the initial fixes; later parser refinements
have focused regressions, the post-fix run, and subsequent actual Android runs.
It is not claimed that every later edit spent two hours under the original soak.

All-thread ten-second host sampling covered the last 4186.7 seconds of the
API 37 endurance capture: peak sampled capture/client RSS was 33852 KiB and
CPU approximately 0.934% of one host core. The final-parser API 37 repeat was
sampled for 891.5 seconds: peak 32380 KiB and CPU 0.948%. API 33/35/36 measured
25840/25828/27384 KiB peak sampled trees, with CPU near 0.94%. API 33 monitoring
covered only the latter portion of its run. Short child peaks and early
unmonitored phases can be missed. These observations and the explicit payload,
queue, cache, analysis and database limits support bounded behavior for the
exercised workloads; they are not a worst-case memory or battery guarantee.

### Acceptance checks, reproduction and limits

The following acceptance checks passed:

- Accepted snapshot constructor/decoder, identity tracker, schema model and
  PLAN.md have no changes against `52ee5b4`.
- Identical snapshots produce identical lifecycle transitions with diagnostics
  absent, malformed, unknown-UID or oversized; only actual snapshot gaps affect
  lifecycle state. No cause is inferred from disappearance.
- Uncertain process association remains explicitly unproven; PID reuse,
  out-of-span timestamps, conflicting exit reasons and clipped analysis results
  retain uncertainty. Source excerpts and references remain bounded.
- Source/analysis/transport boundaries, cancellation behind a full queue,
  failure/recovery, unknown sources and actual 240/256 MiB database boundaries
  have regression proof. Committed records survive the tested size failures.
- Schema-1 replay and schema-2 recording/replay remain compatible. All six final
  runtime recordings replayed exactly with `PATH=/nonexistent`; repeated
  investigation JSON was byte-identical and source hashes were unchanged.
- The two fifteen-minute parser fuzz runs completed 7540440 and 7758419
  executions, respectively, without failure. Focused semantic regressions
  supplement their ownership, encoding, integer and reference invariants.
- `go test ./...`, `go test -race ./...` and `go vet ./...` pass.

Useful reproduction commands (resource tests run one package at a time):

```sh
go test ./...
go test -race ./...
go vet ./...
go test ./internal/evidence -run TestEmulatorProvenanceFixtures -count=1 -v
DROIDSLEUTH_RESOURCE_TESTS=1 go test ./internal/session -run TestActualSQLiteSizeLimit -count=1 -v
DROIDSLEUTH_RESOURCE_TESTS=1 go test ./internal/capture -run TestActualCaptureStopThreshold -count=1 -v
DROIDSLEUTH_SOAK_DURATION=2h go test ./internal/capture -run TestCompatibilitySoak -count=1 -v -timeout=2h5m
go test ./internal/evidence -run '^$' -fuzz '^FuzzDiagnosticParsers$' -fuzztime=15m -parallel=2
```

For runtime reproduction, first review/approve the harness's emulator and app
mutations, build the disposable APK with `scripts/build-validation-apk.sh` into
its expected `/tmp/droidsleuth-compat-apk` directory, then run
`python3 scripts/validate-android-emulator.py API` separately for 30/33/35/36,
or `python3 scripts/validate-android-emulator.py 37 --seconds 7200`.
Runtime durations, not accelerated fixture time, establish endurance evidence.
The harness records exact tested artifacts; future SDK revisions are not assumed
to be identical. No captured user AVD is reused or overwritten.

All task-created AVDs and downloaded API 30/33/35/36 images were removed. Only
the original `Medium_Phone_API_37.0` AVD remains registered, with no task emulator
connected. SDK system-image storage returned to its original 2.7 GiB. Small
private recordings/reports remain under ignored `.validation/compat`; no device
image or raw identifying phone report was committed.

Remaining limits: no new physical phone, OEM-diverse ROM, secondary Android user,
physical USB, device CPU/power or worst-case heavy-stack benchmark was performed.
Production OEM restrictions may differ from AOSP userdebug images even with
shell UID 2000. Silent logcat loss, finite history, delayed/unknown clocks,
unsupported layouts and unavailable identity data remain explicit limitations.
Hot-journal automatic recovery remains outside this gate. Collection fixes apply
to new captures; old recordings are replayed as recorded and their normalized
assertions are not retroactively repaired. Review their retained source evidence.

The gate supports the tested software/runtime scope. Broader physical/OEM and
power validation remains open and requires an authorized real test phone plus
review of exact mutation commands. Stop here; no Milestone 3 work was started.

## Offline incident timelines

This milestone implements the offline investigation slice of PLAN.md §3.B–D
and the illustrative `investigate --last` workflow in §1. The dated
compatibility gate above remains the record for diagnostic source validation.

`investigate --timeline FILE` lists anchors in commit sequence. IDs encode the
observation sequence and zero-based transition or event index. A lifecycle
anchor identifies a recorded disappearance, and a report anchor identifies a
normalized source claim. The index keeps the first occurrence of identical
source reports, while direct lookup accepts any valid observation reference.

`investigate --timeline --incident ID FILE` shows the selected observation
and its context. It includes same-row transitions, so process replacement
remains visible when appearance and disappearance share a snapshot. A
lifecycle view shows the last recorded presence, first observed absence and
snapshot gap count between them when both endpoints are available. It carries
the recorded `after_gap` flag. Diagnostic gaps appear with their own source
and do not contribute to the lifecycle gap count. Candidate associations use
the existing investigation rules and remain unproven.

Context is selected by intersection with the host elapsed collection window
and presented in sequence order. The defaults are 30 seconds on each side,
adjustable independently from zero to five minutes. Device occurrence
timestamps, precision and raw time text remain separate. Retained excerpts
point to one-based lines in the original observation. The index caps at
4,096 anchors, and detail caps at 100 context observations on each side. Large
context rows retain their source and sequence with an explicit omission
marker. An 8 KiB per-row budget covers base context and a 1 MiB per-detail
budget covers report enrichment. Selected excerpts cap at 2 KiB. Truncation
and incomplete association analysis are explicit.

Timeline analysis uses a single read-only SQLite transaction for finalized
schema-1 and schema-2 recordings. This keeps metadata, observation lookup and
scans in the same database snapshot. A file identity, size or modification-time
change detected across the read causes an error. Legacy explicit-file output,
including investigation JSON version 1, remains byte-compatible. Timeline JSON
uses `report_type: "incident_timeline"` and its own version 1. No storage
migration is needed. The capture transport, snapshot tracker, evidence sources
and writer remain unchanged.

`--last` selects the newest finalized managed recording by its timestamped
filename, within a 4,096-entry directory bound. It skips and reports newer
recordings whose metadata is still unfinalized, surfaces unreadable or invalid
candidates, and ignores symlink entries. Explicit filenames can point to
custom output paths. Complete, interrupted and failed finalized sessions are
eligible. The previous binary remains a rollback option without data changes.

### Verification and measurements

`go test ./...`, `go test -race ./...` and `go vet ./...` passed. Regression
coverage includes exact legacy text and JSON, schema-1 and schema-2 reading,
discovery and its errors, replaced files, malformed IDs, terminal quoting,
cancellation, output errors, duplicate and conflicting reports, delayed and
unknown timestamps, same-PID replacement, same-row transitions, boot changes,
window intersections and explicit resource limits.

Offline checks used six retained Android emulator recordings from API 30, 33,
35, 36 and 37. Each index contained recorded disappearance, Java fatal, native
crash and ANR anchors. The 24 selected details and six indexes produced
byte-identical repeated JSON with no ADB on PATH. SHA-256 hashes of the
recordings remained unchanged. Those runs reuse emulator artifacts. An ADB
availability check found no connected phone, so fresh physical validation is
pending.

Benchmarks used the same Linux reference host as the compatibility gate. The
10,000-row input extends the 9,877-row API 37 recording with ordinary source
status rows. The stress input fills the same database with 60 KiB source gaps
until SQLite rejects another row under the 256 MiB page cap. Output JSON was
encoded to a sink. The first run used a page-cache eviction hint for the
database and is not a verified cold boot. The second run used the warm cache.
RSS is sampled Linux process high-water RSS from `/proc`, with possible
undersampling immediately before exit.

| Dataset | Investigation mode | Eviction-hint and warm wall times | Largest sampled RSS |
| --- | --- | --- | ---: |
| 10,000 rows, 4,370,432 bytes | Legacy | 0.148 s / 0.142 s | 24,072 KiB |
| 10,000 rows | Timeline index | 0.074 s / 0.078 s | 23,900 KiB |
| 10,000 rows | Timeline detail | 0.198 s / 0.203 s | 22,540 KiB |
| 14,262 rows, 268,414,976 bytes | Legacy | 1.887 s / 1.902 s | 25,140 KiB |
| Near storage limit | Timeline index | 0.931 s / 0.919 s | 24,428 KiB |
| Near storage limit | Timeline detail | 2.841 s / 2.840 s | 25,180 KiB |

The tested inputs met the two- and 30-second gates, respectively. Peak sampled
detail RSS on the large input was about 40 KiB above the highest legacy sample
and within the 32 MiB incremental budget. This sample is a comparative host
measurement, with no new device polling cost to measure. The index uses one
streaming pass. Detail uses one indexed lookup and one context pass, with up
to two more passes for evidence enrichment.

OEM diagnostic formats, silent cross-buffer loss, physical-device PID
replacement during disconnect, battery and thermal behavior, custom/isolated
processes and automatic hot-journal recovery remain open. Timeline analysis
depends only on the observations already recorded. Historical assertions
retain their original source evidence and uncertainty.

## Per-process CPU and RSS

`watch --resources` writes schema 3 and attaches one resource observation to
each successful snapshot. Plain `watch` writes schema 2. Current readers
accept schemas 1–3. Schema 3 uses the existing SQLite tables and atomic
snapshot append. Earlier binaries reject it as unsupported. Existing files
require no migration.

The optional resource frame reuses the final `/proc/PID/stat` read that
already passed the census and kernel start-tick checks. It represents the
same full boot ID, UID, PID and start-tick identity as the validated process
snapshot. Zero or multiple main-process matches have an explicit status.
Only the first candidate gets two additional best-effort uptime reads in
the batched snapshot command. A successful singleton yields raw stat
fields 14 and 15 in ticks and field 24 in pages. Optional timing or metric
failures do not change the lifecycle result. Mandatory framing, identity or
transport failures still yield snapshot gaps. The disabled command template
matches commit `03d3d37` byte for byte.

One bounded unit probe verifies the snapshot's boot ID on both sides of
`getconf CLK_TCK` and `getconf PAGESIZE` (or `PAGE_SIZE`). Each valid unit is
cached independently until the boot changes. Missing units retry at most
once per minute on eligible snapshots. The steady poll uses the same one
ADB call when units are cached. No host units or fixed device units are
assumed.

Replay exposes raw inputs and independent CPU/RSS availability. Incident
detail derives CPU from the difference in user and system ticks across two
samples of the same identity, divided by device clock ticks per second and
the midpoint elapsed device uptime. The result uses one logical CPU as
100%. It includes the source sequence references and the possible elapsed
interval from the uptime brackets, including uptime's display precision.
Device uptime includes suspend. Accounting and counter quantization also
affect accuracy. An overlapping or reversed interval, a bracket wider
than 250 ms, or a possible interval over five seconds suppresses the rate.
Snapshot gaps, unavailable CPU inputs, resets and identity changes also
prevent a rate across the break. Diagnostic gaps leave CPU continuity intact.

RSS is an approximate resident footprint that can include shared pages.
The timeline independently converts pages to bytes when the unit is known
and the product fits. Last observed presence shows resources for the
identity that disappeared. Context retains measurements for replacement
identities separately. Resource values leave incident IDs, evidence
references and cause attribution unchanged. Existing cancellation,
64 KiB record, 240 MiB stop and 256 MiB database limits still apply.

### Correctness validation

Focused schema, shell, fake-ADB, investigation and CLI tests passed before
runtime validation. They cover observed zeros, independent missing inputs,
decimal timing, a stat-read PID reuse race, disconnect and reboot, counter
decrease, overlapping measurements, oversized intervals and arithmetic
overflow. Fixtures exercise both 4 KiB and 16 KiB page units and CPU above
100%. The full test suite, race suite and vet passed.

On a fresh two-vCPU API 37 Google Play emulator with its ordinary ADB shell,
the approved disposable app touched a 32 MiB direct buffer and ran one-worker
and two-worker CPU phases. All 34 recorder samples matched the identity and
units in 19 independent ADB reads, with 100 Hz ticks and 4096-byte pages.
All 31 recorder samples with independent reads on both sides had cumulative
CPU counters between their two references. The sampler and reference used
different sampling intervals, so their individual percentages are not
pointwise error estimates. The emulator was contended and the two-worker
phase did not reach 100% of one core. Above-100% computation was verified
with fixtures.

Recorder RSS rose from 146.93 MiB just before the allocation to 179.09 MiB
immediately afterward. Both the recorder and independent reference reached
179.86 MiB. Observed stat timing brackets were 10–20 ms, including reported
uptime precision. The provisional 250 ms limit is retained. Its margin on
a physical device remains unmeasured.

The controlled force-stop produced an observed absence with cause unknown.
Its offline detail exposed CPU and RSS at last presence and included the
two CPU source sequences. All 64 schema-3 records replayed exactly with no
ADB on PATH. Repeated detail JSON was byte-identical, the DB hash stayed
unchanged, and the accepted baseline binary rejected schema 3. Physical
validation remains open because no phone was attached.

For backward-compatibility verification, the new reader and `03d3d37` produced
identical replay JSON, timeline indexes and selected details across six
retained schema-2 emulator recordings. Database hashes matched before and
after. Schema-1 coverage uses the existing offline fixtures.

### Overhead compared with `03d3d37`

The reference and candidate were built from `03d3d37` and the source in
`d47fc2a`. Eight alternating command pairs executed the actual generated
snapshot scripts via Android `/system/bin/time -v`. Median paired additional
device user + system CPU was **0.054 ms per snapshot**, with deltas from
−4.657 to +7.894 ms. The provisional 15 ms device CPU target passed on
this emulator. Median command output was 10,962 versus 10,998 bytes.
Shell accounting covers the timed command and its measured work. It does
not quantify battery or thermal cost, and emulator conditions cannot
establish physical-device overhead.

The final live comparison used four uninstrumented 60-second watch windows
in baseline/candidate/candidate/baseline order on the same emulator. The
disposable app was idle and diagnostic collectors were enabled in all four
windows. Each window yielded 58 successful snapshots. Timing, CPU, guest
busy percentage and row size below use the median of the two windows for
each mode. RSS is the maximum across both windows, sampled every 100 ms
over the host capture and its child processes. Host CPU includes waited
ADB clients. The shared ADB server is outside both host measurements.

| Metric | Baseline | Resources enabled | Delta |
| --- | ---: | ---: | ---: |
| Median snapshot interval | 1046.07 ms | 1044.49 ms | −1.58 ms |
| p95 snapshot interval | 1057.40 ms | 1050.97 ms | −6.42 ms |
| Host CPU, percent of one core | 0.989% | 0.950% | −0.039 percentage points |
| Peak sampled host process-tree RSS | 29,960 KiB | 31,236 KiB | +1,276 KiB |
| Median snapshot JSON | 462 bytes | 779 bytes | +317 bytes |
| Final SQLite database | 53,248 bytes | 73,728 bytes | +20,480 bytes |
| Aggregate guest busy CPU | 4.96% | 5.98% | +1.02 percentage points |

The provisional p95, host CPU, RSS and encoded-row growth targets passed.
Differences within measurement variation are not claimed as speedups.
The first comparison used a shim that logged generated scripts. Shimmed
host measurements were excluded from the final comparison because the
shim added host subprocess cost. Device command pairs measured the
generated commands directly. Guest aggregate CPU includes background load
and services and cannot isolate incremental device cost. The steady-state
ADB call count is unchanged with known units, as exercised in the
fake-ADB tests. Missing units may trigger the documented periodic retry.

**Acceptance:** software correctness and provisional emulator overhead
gates passed. The remaining hardware gate is fresh validation on an
authorized physical phone, including device CPU overhead and the observed
timing-bracket distribution. Test captures and raw measurements were kept
under ignored `.validation/resources`.

### Physical CPU/RSS acceptance

The authorized USB phone was a Motorola Android 17/API 37 build with ordinary
ADB shell UID 2000 and Android user 0. The validation package was absent at
preflight. The approved local APK requested no permissions and was installed
without replacement. The only application exercised was the disposable
fixture. The resource action touched a 32 MiB buffer and ran one and then two
CPU workers for 12 seconds each.

Independent ADB reads checked the target UID, PID, start ticks, boot ID,
device uptime, CPU counters, RSS pages and conversion units. The 36 recorder
samples from the workload identity matched the identity and units in 22
reference reads. All 35 samples with a reference on both sides fell within the
reference cumulative CPU counter bounds. The phone reported 100 ticks per
second and 4096 bytes per page.

| Workload | Recorder median CPU, % of one logical CPU | Independent median |
| --- | ---: | ---: |
| One CPU worker | 99.2% | 98.9% |
| Two CPU workers | 199.2% | 199.5% |

The independent and recorder intervals differ, so these medians are a
workload cross-check rather than pointwise error estimates. RSS moved from
140.96 MiB just before page touch to 172.91 MiB afterward. Both readers saw a
peak of 176.64 MiB. RSS remains an approximate resident footprint and can
include shared pages. All workload timing brackets were 10 ms including
reported uptime precision.

Controlled force-stop produced observed absence with cause unknown. Relaunch
produced a distinct process identity and a fresh CPU baseline. Across the
two identities, the capture retained 49 resource samples. The only snapshot
gap in this workload recording was the capture deadline interrupting the
last poll. The workload's 89 schema-3 rows replayed exactly against their
SQLite payloads with ADB absent. Repeated detail JSON matched byte for byte
and the database hash stayed unchanged. The CPU endpoint references and
derived rates were independently checked against the stored raw counters.

The focused fixtures also exercised actual same-PID/different-start-tick
rejection, a boot change with different units, and one resource metric
remaining usable when the other fails. The physical workload did not force
PID-number recycling or deliberately impair a kernel source.

The restarted fixture's median idle CPU was 0% of one logical CPU across 12
rate samples. In a separate capture, a host shim failed three snapshot calls
between successful reads from the live phone. Recovery confirmed the same
full identity and no lifecycle transition. RSS was available on the first
recovered snapshot, CPU returned `baseline_required`, and subsequent valid
samples resumed rates. The three errors were injected at the host transport
boundary. Physical USB disconnection and a real boot change were outside the
executed check. No reboot approval was received. Boot-change and same-PID
replacement semantics remain verified by deterministic fixtures.

#### Physical overhead against `03d3d37`

Four uninstrumented 60-second windows ran in baseline, candidate, candidate,
baseline order. The idle fixture and diagnostic collectors stayed active
throughout. Baseline counts were 48 and 52 snapshots. Both candidate
windows had 50. No snapshot gaps occurred in these windows. Cadence is
snapshot start-to-start. Command latency is the host-observed elapsed time
of a successful snapshot ADB shell call. Per-mode figures are medians of
the two windows, except RSS, which is the maximum sampled process-tree RSS.
Host CPU includes waited ADB clients. RSS includes sampled children of all
host threads at 100 ms intervals and may miss short peaks. The shared ADB
server is excluded from both host measurements.

| Metric | Baseline | Resources enabled | Delta |
| --- | ---: | ---: | ---: |
| Median snapshot cadence | 1214.36 ms | 1181.49 ms | −32.87 ms |
| p95 snapshot cadence | 1248.13 ms | 1306.51 ms | +58.38 ms |
| p95 snapshot command wall latency | 236.75 ms | 298.95 ms | +62.19 ms |
| Host CPU, percent of one logical CPU | 0.994% | 0.980% | −0.015 percentage points |
| Peak sampled host process-tree RSS | 32,996 KiB | 33,076 KiB | +80 KiB |
| Median encoded snapshot | 470 bytes | 799 bytes | +329 bytes |
| Final SQLite database | 47,104 bytes | 69,632 bytes | +22,528 bytes |
| Aggregate phone busy CPU | 6.71% | 9.95% | +3.25 percentage points |

Twelve interleaved command pairs alternated candidate-first and baseline-first
order. Android `/system/bin/time -v` measured user plus system CPU for the
actual generated snapshot scripts. Median costs were 138.883 ms baseline
and 140.250 ms candidate. The median of paired differences was **+1.747 ms**,
with individual differences ranging from −8.745 to +18.485 ms. Median
command stdout grew from 26,355 to 26,396.5 bytes. The 15 ms target applies
to the median paired difference. One pair exceeded 15 ms. Aggregate phone
busy CPU includes background load and ADB service work and is not an isolated
overhead estimate. The baseline command wall p95 itself ranged from 181.53
to 291.98 ms across its two windows. The measured wall p95 increase has no
separate provisional target, and two windows per mode do not isolate the
source of that difference.

| Provisional target | Physical result | Decision |
| --- | --- | --- |
| Median paired device CPU ≤15 ms | +1.747 ms | Pass |
| p95 cadence growth ≤max(10%, 20 ms) | +58.38 ms (4.68%), allowance 124.81 ms | Pass |
| Added host CPU ≤0.5 percentage points | −0.015 percentage points | Pass |
| Added peak sampled host RSS ≤8 MiB | +80 KiB | Pass |
| Added encoded data ≤1 KiB per snapshot | +329 bytes | Pass |
| Extra steady-state ADB calls with cached units | Zero. Instrumented workload saw one initial unit probe. | Pass |

In the idle candidate windows, 98 timing brackets measured 10 ms and two
measured 20 ms. No exercised bracket approached 250 ms. The cutoff and all
provisional targets are unchanged. A new boot or a rate-limited retry after
missing units can add a probe outside steady operation with cached units.
This run does not quantify battery, thermal or other OEM behavior.

**Acceptance:** the tested physical workload, independent input checks, process
replacement, host-injected recovery, offline analysis and provisional
performance targets passed. Physical boot change remains unverified for this
gate. The disposable package was uninstalled and verified absent. No product
code change was needed. `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. Raw artifacts are in ignored
`.validation/resources-phone`.

## Terminal-first incident investigation

The interactive slice of PLAN.md §3.E is available through
`investigate --tui (FILE | --last)`. It uses the same finalized recording
reader, incident index, timeline facts and association analysis as the CLI.
`--incident ID` opens a particular timeline, with optional initial
`--before` and `--after` bounds. The browser is opt-in. The CLI's
text and JSON formats and schemas 1–3 remain compatible. It needs a TTY
on stdin and stdout. Redirected users can use `--timeline` or
`--timeline --json`.

The incident index supports navigation, paging and direct entry of a bounded
canonical ID beyond the index cap. The selected timeline marks the anchor,
retains recording sequence and keeps host collection bounds distinct from
device occurrence timestamps. Users can scroll the context, cycle between
30-second, two-minute and five-minute symmetric windows, and inspect any
retained observation. The view exposes source-specific coverage gaps,
omitted context, incomplete analysis, conflicting reports and unproven
instance associations. Snapshot and lifecycle details show CPU as a percentage
of one logical CPU with interval bounds and endpoint sequences. RSS is
approximate and its availability is independent of CPU. Disappearance remains
cause unknown. Resource samples and nearby reports provide context.

The evidence inspector reopens the finalized file read-only and checks each
requested sequence and exact one-based range against an event in the stored
record. It shows the stored source, format, line numbers and capture truncation.
Untrusted recording text is escaped before wrapping or styling, including
C0/C1, DEL, escape sequences and bidi controls. Backslashes in source lines
are escaped so their original occurrence is distinguishable from displayed
control escapes.

Bubble Tea v1.3.7 handles keyboard input, resize events and the alternate
screen. Lip Gloss v1.1.0 styles the header. The pinned graph's terminal and
ANSI packages handle raw terminal state and cell widths. The browser pauses
navigation below 40 columns by 10 rows and keeps the current selection
through resize. Quit, Ctrl+C, Ctrl+D, terminal EOF, SIGTERM and errors all
leave the loader cancelled. Terminal setup and output failures unwind
through a terminal restoration path. An unreadable initial recording exits
with a diagnostic. Recoverable detail or evidence errors allow Back or Retry.

The browser retains at most the existing 4,096 index entries, one detail
with 100 observations per side, and one stored source slice from an
8 KiB evidence record. Related links are bounded by the existing per-record
and per-finding limits. Only viewport rows are rendered for the index and
timeline. One worker performs a read at a time, with a single pending slot
for the latest request. Completed reads from abandoned selections are
discarded. Each read checks the opening file's identity, size and
modification time before and after the read, in addition to the
transaction's checks. A detected replacement requires reopening.

### Verification

All three implementation phases passed focused tests. The final
`go test ./...`, `go test -race ./...`, `go vet ./...` and
`go mod verify` passed on Linux with Go 1.23.12. The complete selected module
graph resolves on Go 1.23 and no module declares a later Go version.
Deterministic tests cover index and timeline selection, window changes,
verified evidence range rejection, independent resource availability,
unproven associations, diagnostic isolation, terminal escaping, stale
results, retry, cancellation and file replacement. A synthetic schema-1
recording also passed the terminal lifecycle workflow.

The Linux PTY smoke test in `scripts/validate-tui.py` passed on a retained
API 37 emulator diagnostic recording and both retained schema-3 workload
recordings. It exercised opening an incident by ID, evidence selection,
resize to a 20×5 terminal and back, keyboard navigation through `--last`,
normal quit, Ctrl+C, Ctrl+D, SIGTERM and the unreadable-recording path. It
checked alternate-screen exit and exact terminal attribute restoration.
A separate run checked the resource context on the retained schema-3
phone recording. These are offline checks on existing artifacts.

With ADB absent from PATH, eight retained files produced byte-identical
legacy and timeline text/JSON indexes against commit `6e283e0`. Repeated
selected timeline JSON also matched for each recorded incident kind.
Those files include API 30, 33, 35, 36 and 37 emulator data, one emulator
CPU/RSS workload, and one historical physical API 37 CPU/RSS workload.
All eight retained files kept their SHA-256, sizes and modification times.
No fresh emulator or phone capture was required.

### Performance

Both binaries were built with Go 1.23.12 on the Linux x86-64 reference host
(AMD Ryzen 7 4800H). The representative benchmark uses the retained API 37
emulator session extended to 10,000 rows with ordinary source-status rows
(4,370,432 bytes). The near-limit case adds 60 KiB synthetic diagnostic
gaps until the 256 MiB SQLite page cap stops inserts (14,262 rows,
268,414,976 bytes). Both cases inspected the same lifecycle anchor.
The database files were temporary and warm in the OS cache.

Five pairs per mode and input alternated CLI-first and TUI-first.
CLI measurements use `6e283e0` with equivalent timeline JSON output to a
sink, timed to process exit. TUI measurements use process launch through
the first loaded PTY frame, including presentation. The RSS figures are
the largest Linux `VmHWM` readings sampled about every 2 ms. Exit can race
the last sample. All results reflect this host and workload.

| Input and view | CLI median | TUI median (max) | CLI peak RSS | TUI peak RSS | RSS delta |
| --- | ---: | ---: | ---: | ---: | ---: |
| Representative index | 0.108 s | 0.120 s (0.121 s) | 21,392 KiB | 22,576 KiB | +1.16 MiB |
| Representative detail | 0.308 s | 0.305 s (0.321 s) | 21,876 KiB | 22,936 KiB | +1.04 MiB |
| Near-limit index | 2.059 s | 2.087 s (2.103 s) | 21,620 KiB | 22,540 KiB | +0.90 MiB |
| Near-limit detail | 6.186 s | 6.206 s (6.372 s) | 22,568 KiB | 24,488 KiB | +1.88 MiB |

With the near-limit detail already loaded, 60 keyboard movements had
17.23 ms p95 from PTY input to the updated selection. Sixty resizes
between 110×28 and 78×18 had 17.20 ms p95 to the next visible frame.
These timings include the renderer's frame scheduling. Five quits initiated
during a near-limit load completed in 15.62–15.98 ms, including screen and
terminal restoration. A deterministic rapid-window test observed one active
worker, one latest pending request, cancellation of the superseded worker
and suppression of its result.

All measured loads met the approved 2-second representative and 30-second
near-limit limits. Both frame p95s were below 50 ms. Peak sampled RSS rose
by at most 1.88 MiB against the 16 MiB allowance. Raw artifacts, the
measurement harness and the verified module graph are in ignored
`.validation/tui`.

### Remaining limitations

Large index and detail requests still scan the recording through the existing
investigation API. Window changes reload those facts in the background.
Context, analysis and evidence limits can omit information. The UI retains
their omission and truncation indicators and points to CLI replay for stored
observations. Live recording, search, dashboards and hot-journal recovery
remain outside this milestone. The underlying diagnostic coverage and Android
compatibility limits described above still apply.

## v0.1 capture-to-investigation handoff

`watch --summary` uses the existing post-commit callback to announce readiness
on stderr once, following the first successful snapshot. In this mode stdout is
reserved for the incident summary. The capture can be ready while the selected
process is absent. Duration expiry and an interrupt both finalize through the
existing writer path. The summary begins only after capture and finalization
succeed. Plain `watch` keeps its per-record presentation.

The handoff scans the exact absolute recording path through the finalized
`investigate.Timeline` index with a fresh 10-second analysis deadline. It
prints the package, schema, finalized status, and at most five anchors in
reverse index order, with host collection bounds. A capped index warns that
later anchors can be omitted. An empty index describes the stored result and
points to replay for coverage. A disappearance has an unknown cause and a
diagnostic report has unproven instance association. The printed TUI, text,
and replay commands quote the exact recording path, including embedded
single quotes. Paths with terminal controls or invalid UTF-8 use byte escapes
in Bash or Zsh command syntax.

Capture or finalization errors skip the summary. Errors after finalization
identify the saved path and return nonzero so the user can retry with
`investigate FILE`. The handoff uses the existing read-only SQLite
transaction, index limit, and schema readers. Capture keeps its existing
ADB calls, collectors, identity tracker, gap semantics, and recording
formats. `--last` remains available for separate investigation commands.

### Verification and rehearsal

- Fake-ADB tests covered duration completion, interrupted capture, readiness
  after a failed poll followed by a committed empty snapshot, exclusive
  output creation, injected metadata finalization failure, and summary
  output failure. The paired timed captures issued the same counts by ADB
  operation and retained the same observations per source after removing
  run-specific timing and sequence envelopes.
- Finalized fixtures covered schemas 1–3, empty and capped indexes, delayed
  and duplicate reports, PID reuse across a snapshot gap, diagnostic gaps,
  unavailable resources, malformed rows, and an expired analysis deadline.
  Printed IDs resolved through existing incident details. Copies of summary
  commands round-tripped paths with spaces, quotes, control characters, and
  invalid UTF-8 through the shell. Repeated summary output matched and the
  recordings kept their SHA-256 hashes.
- With ADB absent, replay, investigation, timeline text and JSON, and selected
  incident details matched commit `e5f572c` byte for byte on a retained
  emulator diagnostic recording, retained emulator and historical physical
  CPU/RSS recordings, and the new rehearsal recording. Hashes stayed unchanged.
  A retained Java fatal recording also passed the Linux TUI PTY smoke test.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and
  `go mod verify` passed with Go 1.23.12.

A clean checkout of `4f32ff6` built with Go 1.23.12 and ran the quickstart
workflow on a disposable API 37 Google Play x86-64 emulator with ordinary shell
UID 2000 and KVM. The approved app package was verified absent before install.
The timed idle run produced a finalized schema-2 empty index. An unavailable
serial produced a startup error and created no database. For the schema-3 run,
the fixture triggered a controlled Java exception, relaunched as a different
process, and was force-stopped. SIGINT finalized the recording as interrupted.

The summary showed exit-history anchor `E24.0` for Android reason 4
(`APP CRASH(EXCEPTION)`) and separate disappearance anchor `L25.0` for the
force-stopped instance. The crash association remained unproven. The
disappearance cause remained unknown. Both anchors opened in text and the TUI
with ADB absent and the recording hash unchanged. The printed text and replay
commands executed against the quoted path as displayed.

Independent ADB inspection showed the fixture's Java fatal stack in the
emulator crash buffer. The recording's bounded logcat polls retained zero
reports in this run. The cause of that source miss was unverified. The
handoff presented the recorded exit-history evidence. This is an emulator
result, with previous physical evidence used only for offline compatibility.
The fixture was uninstalled, the AVD was removed, and no physical-device
workload ran.

### Performance and acceptance

Five paired warm-cache runs per input alternated standalone timeline JSON and
summary. A temporary Go 1.23.12 harness exercised both production paths
against pre-finalized SQLite inputs with output directed to a sink. Timing
starts immediately before the offline operation and excludes capture and
process startup. Linux `VmHWM` was sampled about every 2 ms. Short peaks
near process exit can escape sampling. This isolates the handoff and does
not measure the memory retained by a prior live capture in the same process.
The host was Linux x86-64 with an AMD Ryzen 7 4800H.

| Input | Standalone timeline latency range | Summary latency range | Standalone peak RSS | Summary peak RSS | RSS delta |
| --- | ---: | ---: | ---: | ---: | ---: |
| 10,000 rows, 4,370,432 bytes | 0.097–0.108 s | 0.096–0.111 s | 22,740 KiB | 22,792 KiB | +52 KiB |
| 14,262 rows, 268,414,976 bytes | 2.020–2.135 s | 2.014–2.252 s | 23,360 KiB | 23,240 KiB | −120 KiB |

The representative input extends a retained API 37 emulator recording with
source status rows. The near-limit input adds synthetic 60 KiB diagnostic gaps
until the 256 MiB SQLite page cap rejects another insert. All runs passed the
2-second representative, 10-second near-limit, and 16 MiB incremental RSS
targets. The fixture comparison observed zero added ADB calls. Benchmarks and
rehearsal artifacts are in ignored `.validation/handoff`.

The handoff scans the finalized file once, so a slow or damaged recording may
require explicit-file retry. The summary's five entries come from the bounded
index, and retained reports can be delayed or absent. Repository-wide
compatibility and diagnostic source limitations above still apply.

## v0.1 release-readiness gate

### Java fatal evidence on API 37

The historical handoff recording retained seven successful logcat polls with
zero selected observations. Android exit history and an independent
crash-buffer inspection identified the Java crash. That run retained no raw
poll replies, so the exact original retrieval path remains unverified.

The release gate reproduced the same symptom on commit `a4f4864` with the
API 37 Google Play x86-64 image (build 16418371, fingerprint
`google/sdk_gphone64_x86_64/emu64xa:17/CE2A.260420.051.A2/16418371:user/release-keys`),
ADB 1.0.41, and ordinary shell UID 2000. Three ordinary fixture crashes
had an exit-history report and lacked a corresponding stored Java fatal.
One run used the uninstrumented collector. In the matched instrumented run,
four consecutive actual merged numeric-tail reads after the crash returned
zero bytes with exit status 0. Crash-only replies taken 17–28 ms later
each contained the matching stack. A separate exact merged request caught
the same stack closer to the crash, and device-time cursors covering the
crash returned it in both buffer sets. The results locate the reproduced
failure at retrieval. Parsing, deduplication, and persistence received
no bytes for those missing reports.

The AOSP log reader selects a finite tail from the requested buffers before
logcat's tag display filter. A reported success with zero selected lines
therefore provides no evidence about the unfiltered volume or the retention
of a particular crash. Poll timing determines whether a merged numeric tail
still contains the stack. The affected emulator's crash-only buffer retained
it through the missed polls. [AOSP logcat implementation](https://android.googlesource.com/platform/system/logging/+/refs/heads/main/logcat/logcat.cpp)

Commit `68c696c` adds a separate 512-entry crash-buffer tail to numeric
logcat polls. The two reads share one ADB invocation and the existing boot
bracket, transport deadline, and output budget. A fixed frame separates the
two inputs so the parser cannot assemble a stack across reads. Identical
evidence is deduplicated before the observation limit. A failed crash-only
read creates a diagnostic coverage gap and can still retain independently
verified merged data. A missing frame, transport failure, or failed boot
bracket withholds the affected reply. The merged timestamp-cursor path still
runs a single logcat command per poll. A crash-tail line can seed a cursor
when the merged tail has no decodable line. The observation schema and
lifecycle, CPU/RSS, and attribution rules are unchanged.

To reproduce the device comparison, use the task-owned disposable fixture and
an approved emulator. During an ordinary fixture Java crash, capture the
stock poll trace and sample the following pairs as close together as possible.
Use the same device epoch interval in both cursor probes and alternate which
buffer list is read first. Retain the exit status, host timing, and only the
matched fixture lines. Inspect the capture with `replay --json` for
`source_status` and source coverage gaps.

```sh
# Substitute the verified emulator serial and a device-epoch start before the crash.
serial=emulator-5584
cursor=1700000000.000000
adb -s "$serial" shell -T "logcat -d -t 512 -v threadtime -v epoch -v uid -v usec -b main -b crash -b system -b events 'AndroidRuntime:E' 'DEBUG:F' 'am_crash:I' 'am_anr:I' '*:S'"
adb -s "$serial" shell -T "logcat -d -t 512 -v threadtime -v epoch -v uid -v usec -b crash 'AndroidRuntime:E' 'DEBUG:F' 'am_crash:I' 'am_anr:I' '*:S'"
adb -s "$serial" shell -T "logcat -d -T '$cursor' -v threadtime -v epoch -v uid -v usec -b main -b crash -b system -b events 'AndroidRuntime:E' 'DEBUG:F' 'am_crash:I' 'am_anr:I' '*:S'"
adb -s "$serial" shell -T "logcat -d -T '$cursor' -v threadtime -v epoch -v uid -v usec -b crash 'AndroidRuntime:E' 'DEBUG:F' 'am_crash:I' 'am_anr:I' '*:S'"
go test ./internal/evidence -run 'Test.*(CrashTail|NumericLogcat|NumericTails)' -count=1
```

The regression fixture preserves the relevant captured headers with normalized
time, PID, UID, and package. Tests assert retention from an otherwise empty
merged read, ADB call count, the transition to the existing cursor path,
duplicate suppression, independent frame parsing, and rejection of malformed
or unverified input. Raw trial traces and the temporary harness are local
under ignored `.validation/v01`.

### Release acceptance

The final collector captured a Java fatal for the matching PID in all three
ordinary API 37 trials. Their report anchors were `E43.0`, `E44.0`, and
`E46.0` in their respective recordings. A separate force-stop produced
`L43.0` with an unknown disappearance cause. The same recovery approach also
captured the current Java fatal in an observed timestamp-cursor trial and
during a bounded 1,600-message burst of unrelated logs on API 37.

The API 30 AOSP x86-64 smoke recorded the fixture's Java fatal and
disappearance with shell UID 2000. The fresh emulator also produced an
empty finalized recording before the first launch. The unavailable-serial
startup check created no database. Automated checks cover diagnostic
degradation, startup and finalization failures, cancellation, and the
empty summary path. The API 30 app, AVD, and temporarily downloaded system
image were removed after the smoke.

The exact-file handoff opened all four API 37 acceptance anchors through
text and the TUI with ADB absent, including the matching report, uncertainty,
and terminal restoration. SHA-256 fingerprints were unchanged. The
baseline and candidate produced identical repeated replay, investigation,
timeline, and selected detail output on identical schema-1, schema-2,
and schema-3 inputs. The comparison included a synthetic schema-1 file,
retained emulator and historical physical recordings, and three new
API 30/37 recordings. The physical recording was used offline.

### Collector cost

The API 37 emulator ran matched baseline/candidate/candidate/baseline windows
with an idle fixture and again with one 1,600-message unrelated burst per
window. The windows were 45 seconds each, shorter than the proposed five
minutes. Each window contained 43 successful snapshots and no snapshot
coverage gaps. Host CPU includes the capture process and its waited ADB
children. RSS is the peak of the process tree sampled at 100 ms intervals.
The table uses the median of the two window p95s and CPU values per mode,
and the largest sampled RSS.

| Condition | Build | Snapshot cadence p95 | Host CPU, one core | Peak RSS | Database bytes |
| --- | --- | ---: | ---: | ---: | ---: |
| Idle | Baseline | 1056.31 ms | 1.490% | 32,168 KiB | 73,728 |
| Idle | Candidate | 1053.33 ms | 1.461% | 27,720 KiB | 139,264 |
| Unrelated burst | Baseline | 1056.08 ms | 1.458% | 36,352 KiB | 73,728 |
| Unrelated burst | Candidate | 1059.27 ms | 1.494% | 26,928 KiB | 139,264 |

Each window issued 15 logcat ADB calls. The baseline used numeric tails
throughout, while the candidate used one pair of numeric tails within one
ADB call followed by 14 timestamp-cursor reads. The larger candidate
databases retained older crash stacks and additional diagnostic coverage
gaps. Every candidate window had 16 logcat gaps versus two baseline gaps.
The candidate recorded a malformed-data gap on each cursor read. A scoped
trace of that cursor behavior showed the API 37 PID-first `am_crash` layout
already rejected by the parser. The layout remains unsupported. The gaps
are diagnostic and leave snapshot continuity intact.

In eight interleaved numeric-command pairs, Android `time -v` measured
median user plus system CPU of 10.23 ms for the merged tail and 22.48 ms
for the combined command. The median paired increase was 11.90 ms.
Median host-observed wall time was 63.17 ms and 77.90 ms respectively.
The command timing excludes work charged to separate Android services.
Its output increased from zero to a median 26,277 bytes on this recording
history. This measures the additional numeric read on the disposable emulator.
It does not establish physical-device CPU, battery, or thermal costs.

Cadence, host CPU and RSS stayed within the release limits. Go 1.23.12
passed `go mod verify`, `go test ./...`, `go test -race ./...`, and
`go vet ./...`. Both disposable AVDs were removed, and no physical-device
workload ran.

### Local release candidate

**Verdict: release-readiness gates passed for the tested scope.** The
reproduced ordinary Java fatal loss is fixed for the tested numeric-tail
scenario. Finite retention, format differences, and incomplete source
coverage remain documented limits.

`scripts/package-release.sh NEW_OUTPUT_DIRECTORY` builds from a clean local
commit with Go 1.23.12 and verified module dependencies. It emits a
Linux amd64 tarball and `SHA256SUMS`. The archive contains the CLI, README,
license, dependency notices, release notes, and source and toolchain
metadata. It uses normalized archive ordering, ownership, modes, and
timestamps. Local build logs and artifacts are kept under ignored
`.validation/v01`.

## v0.2 retained exit-history collection

`collect-history --out FILE [--device SERIAL] [--user ID] PACKAGE` takes one
package-scoped Android exit-history reading. The command uses the existing
device, SDK, user, and package UID probes and shares the timezone and history
read with `watch`. It requires the package to be installed for the selected
user. The output path is explicit and the store reserves it exclusively.

The command writes schema 2. Its scope row identifies retained history and
the lack of live process, resource, and logcat observations. A source-status
row records the read outcome, followed by any diagnostic gaps and verified
reports. Host bounds describe retrieval. Android occurrence times and
reported reasons remain source claims. Exit reports retain no boot ID or
proven process instance and generate no lifecycle transitions.

A supported empty result finalizes as `complete`. Source failures and limits
return a nonzero exit status and preserve diagnostic gaps and any accepted
reports. An interrupted collection keeps its committed observations and
finalizes as `interrupted`. The CLI prints the path, count, and an explicit
timeline command whenever it can open the writer. Existing SQLite size and
privacy rules apply. The command uses a 25-second acquisition deadline and
bounded writes and finalization.

The shared parser previously resolved an empty device timezone as UTC because
of Go's `time.LoadLocation` behavior. A reproducing test also checks that
`Local` does not resolve against the host timezone. Missing, invalid, host-local,
or ambiguous timezone data keeps the raw date string and leaves occurrence
time unknown. This applies to new exit-history observations in both commands.

### Verification

- Fake ADB verifies six calls with default user selection, with no snapshot or
  logcat commands. Tests cover package and UID filtering, multiple devices,
  missing packages, exclusive output, malformed and denied history, an
  unsupported source, a 4 MiB output overflow, ambiguous wall time,
  cancellation, and deadline expiry.
- A 128-entry fixture with large escaped excerpts retained 64 reports in a
  3,158,016-byte finalized recording. The capture recorded the observation
  limit as a diagnostic gap and returned nonzero.
- API 30 AOSP and API 37 Google Play x86-64 disposable emulator runs first
  checked that a fresh fixture returned empty history. Each run then launched
  the fixture in its controlled Java fatal mode. Independent scoped ADB reads
  confirmed the PID had exited and Android retained reason 4 before the first
  post-exit collection. No live DroidSleuth recorder was running.
- Ten post-exit collections per API retained the matching report. Offline
  investigation, with ADB absent, returned the verified evidence range and
  an unbound instance. Repeated JSON and database hashes stayed the same.
  Both APIs passed the Linux TUI PTY smoke test for evidence navigation and
  terminal restoration.
- Replay and investigation output for identical schema 1–3 inputs matched
  the published v0.1.0 executable byte for byte. Matched fake-ADB `watch`
  captures issued the same call counts and retained equal per-source
  observations after normalizing host timestamps and sequence numbers.
- Go 1.23.12 passed `go mod verify`, `go test ./...`, `go test -race ./...`,
  and `go vet ./...`.

### Measured cost

The ten one-shot runs per API used the same retained fixture exit. Timing
included CLI startup, all ADB calls, database finalization, and CLI output.
The table reports nearest-rank p95 and peak host process-tree RSS sampled
about every 2 ms. The shared ADB server is outside the measured process
tree. Short peaks can escape sampling.

| Emulator | Runs | Completion p95 | Peak host RSS | Recording size |
| --- | ---: | ---: | ---: | ---: |
| API 30 AOSP x86-64 | 10 | 116.3 ms | 25,204 KiB | 12,288 bytes |
| API 37 Google Play x86-64 | 10 | 210.9 ms | 18,700 KiB | 12,288 bytes |

Both runs passed the 10-second and 48 MiB targets. Live `watch` overhead
was not remeasured. The matched fixture confirmed its ADB call counts,
and the shared read uses the same shell commands and bounds. The
runtime proof is emulator-only. OEM permissions, retention, package
removal, and UID changes can limit available history. Local artifacts
are under ignored `.validation/v02`.

## v0.2.0 release-readiness gate

Reviewed against `0de023d` with Go 1.23.12 on Linux amd64. Regression tests
first reproduced signed nanosecond wraparound from an out-of-range `UnixNano`
conversion, a malformed or unsupported body reported as a successful empty
read, and adjacent package section text in the selected excerpt. The parser
now leaves unrepresentable instants unknown, rejects unrecognized empty
layouts and malformed entry headers, and ends excerpts at package or UID
section boundaries. Both capture modes share the fix. It adds no ADB calls.

### Correctness and compatibility

- Fake ADB exercised current and explicit Android user selection, matching
  package and UID, cross-user reports, package-prefix lookalikes and another
  package sharing the selected UID. A failed timezone probe retained the
  selected report and raw timestamp. Parser fixtures covered an invalid or
  blank zone, `Local`, DST folds and spring gaps, malformed dates, signed
  nanosecond bounds and a valid non-UTC conversion under different host
  zones. Before this gate the date `2500-01-01 00:00:00.000` yielded
  `-1721518473709551616` ns. It now yields unknown occurrence time.
- Genuine empty reads finalized complete. Unrecognized, malformed and
  denied output, the 4 MiB transport limit and the 128-entry parser limit
  yielded explicit gaps and failed captures. Over-budget observations left
  other accepted reports intact. The 64-report fixture exercised bounded
  partial success. A report truncated at 8 KiB retained an excerpt-truncated
  gap. SQLite tests exercised the existing 240 MiB stop and 256 MiB page
  limits. History-only files were private and used schema 2 with no lifecycle
  transitions, snapshot or resource samples, boot identity, or proven
  process-instance attribution.
- The default path made six ADB calls. Explicit user selection made five.
  Source-call timeout and the 25-second acquisition deadline retained their
  existing limits. A blocked fake-ADB read finalized failed in 3.033 s.
  In a separate 64-report fixture, SIGINT was sent after the reader observed
  nine committed reports. Ten reports remained replayable, the CLI printed
  ten stored reports and the recording finalized interrupted in 193.6 ms
  including acquisition and offline verification.
- `go mod verify`, `go test ./...`, `go test -race ./...`, and
  `go vet ./...` passed using Go 1.23.12. Matched fake-ADB captures against the
  published v0.1.0 binary covered plain `watch`, `--resources`, and
  `--summary`, each with duration-driven completion and SIGINT. Normalized
  per-source observations matched, including a retained exit report. Both
  binaries used 12 calls per plain or summary run and 13 per resource run.
- The checksum-verified v0.1.0 release binary and the candidate produced
  identical replay, investigation text/JSON, incident indexes and selected
  detail JSON across schema 1–3 inputs, including both new history-only
  recordings. Linux PTY smoke tests exercised incident and evidence
  navigation, resizing, terminal restoration after keyboard or signal
  exits, and `--last`. Offline tests used an unavailable ADB path and
  preserved recording fingerprints.

### Runtime evidence and measured cost

Host ADB listed no devices at preflight. No physical phone was used. Only
the approved disposable APK was installed and deliberately crashed on fresh
emulators. Each case started with empty history and independently confirmed
process exit, package UID, Android reason 4 and raw wall time before matching
the persisted report. Both APK installs were removed. The temporary AVDs
and downloaded API 30 image were removed after the gate.

| Emulator image | Fingerprint | Post-exit runs | Completion p95 | Peak sampled host RSS | Recording size |
| --- | --- | ---: | ---: | ---: | ---: |
| API 30 AOSP x86-64 | `Android/sdk_phone_x86_64/generic_x86_64:11/RSR1.210722.013.A2/10067904:userdebug/test-keys` | 10 | 82.0 ms | 25,416 KiB | 12,288 bytes |
| API 37 Google Play x86-64 | `google/sdk_gphone64_x86_64/emu64xa:17/CE2A.260420.051.A2/16418371:user/release-keys` | 10 | 115.4 ms | 23,272 KiB | 12,288 bytes |

Nearest-rank p95 includes CLI startup, ADB subprocesses and finalization.
RSS samples were taken about every 2 ms and exclude the shared ADB server
and the emulator. Both runs passed the 10-second and 48 MiB targets. The
shared exit-history parser was also benchmarked in baseline/candidate/
candidate/baseline order with one API 37 entry and `GOMAXPROCS=1`. Median
time moved from 129.3 to 133.3 microseconds over six samples each. Both
versions allocated 9,016 bytes in 138 allocations per parse. This isolates
host parser cost. No end-to-end live `watch` CPU estimate is implied.

Runtime evidence for the new workflow covers these two emulator images.
Older API 33/35/36 emulator and API 37 physical evidence relates to earlier
capture milestones. Actual retention and OEM support vary. Package removal
or a UID change can make older reports unavailable, and historical timezone
changes cannot be recovered from the retrieval probe. Earlier repeated
API 37 `am_crash` diagnostic gaps and finite-buffer logcat loss remain
documented limitations.

### Local release candidate

The package target is Linux amd64 with `GOAMD64=v1` and CGO disabled. Run
`scripts/package-release.sh NEW_OUTPUT_DIRECTORY` from two independent
clean checkouts of the release commit with Go 1.23.12. Compare both archive
and manifest bytes, run `sha256sum -c SHA256SUMS`, and verify archive
permissions, source and Go metadata, and third-party notices. Follow the
README install steps into an isolated prefix and exercise replay, text/JSON
investigation and the TUI with ADB absent. Local runtime, benchmark, parity,
and packaging logs are under ignored `.validation/v020-gate/`.

**Verdict: GO for local v0.2.0 release candidate review.** Required gates use
the emulator-validated scope above. Publication requires separate approval.
