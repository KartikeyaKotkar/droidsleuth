# DroidSleuth v0.2.0

Use `collect-history` after an app has already exited. It saves available
Android exit reports to a new schema-2 recording for offline investigation.
The package must still be installed for the selected Android user.

```sh
droidsleuth collect-history --out incident.db com.example.app
droidsleuth investigate --timeline incident.db
droidsleuth investigate --tui incident.db
```

Missing or ambiguous device timezone data and unrepresentable dates leave
the occurrence instant unknown. The recording preserves Android's raw time
string. Unsupported or malformed history is reported as incomplete coverage.
Android's reported reason and PID remain source claims with unproven
historical process-instance attribution.

## Install

Download `droidsleuth-v0.2.0-linux-amd64.tar.gz` and `SHA256SUMS` together.

```sh
sha256sum -c SHA256SUMS
tar -xzf droidsleuth-v0.2.0-linux-amd64.tar.gz
install -Dm755 droidsleuth-v0.2.0-linux-amd64/droidsleuth "$HOME/.local/bin/droidsleuth"
```

The archive includes its commit, Go 1.23.12 build information, and dependency
notices. To build from source, run `go build -o droidsleuth ./cmd/droidsleuth`
with Go 1.23 or later.

## Tested scope

- Linux amd64. Collection uses Android SDK Platform-Tools and an authorized
  device running Android 11/API 30 or later with accessible diagnostic sources.
- The v0.2.0 post-exit workflow passed on API 30 AOSP and API 37 Google Play
  x86-64 emulators. Earlier validation covered API 33, 35, and 36 emulators
  and one historical API 37 phone. This gate had no physical phone.
- Offline replay and investigation read schemas 1–3. Plain `watch` and
  `collect-history` write schema 2. `watch --resources` writes schema 3.
  Replay, text/JSON investigation, and the TUI were verified with ADB absent.

## Known limitations

- Retained history can expire or arrive late. Package removal and UID changes
  can make earlier reports unavailable. OEM formats and permissions vary.
  DroidSleuth reads the timezone at retrieval. It may differ from the
  historical timezone.
- Finite buffers and bounded filtered logcat reads can miss reports. The
  API 37 PID-first `am_crash` event format remains unsupported. Earlier
  release testing recorded repeated diagnostic gaps for that format. Exit
  history may supply independent evidence when Android retains it.
- Process disappearance alone leaves the cause unknown. Lifecycle tracking
  covers the main process. Secondary, custom-named, and isolated processes
  are outside its scope.
- Recordings and exports can contain sensitive identifiers, timings, and
  selected app diagnostics. Review them before sharing.

See the README for workflows and troubleshooting and IMPLEMENTATION.md in the
repository for validation evidence and measurements.
