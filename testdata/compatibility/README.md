# Compatibility fixture provenance

These snippets were reduced from disposable-app observations on the gate's real
Android emulators: API 30 AOSP x86_64 and API 37 Google Play x86_64. They are
parser regression fixtures, not independent physical-device evidence.

Times, package names, UID/PID/TID values, native thread names/addresses and ANR
reason text are normalized. Native optional `ppid` presence, standalone UID,
signal markers, Java fatal/process headers and event field ordering are retained.
The rejected `am_crash` examples reconstruct the observed PID/user ordering with
normalized identifiers; they intentionally remain unsupported. No OEM support
claim is inferred from either AOSP image.

The existing `../logcat-device-native.txt` came from the earlier physical-phone
milestone. Its provenance remains physical/historical, separate from this gate.

Reproduce parser checks with:

```sh
go test ./internal/evidence -run TestEmulatorProvenanceFixtures -count=1 -v
```

Full runtime source availability, image revisions, endurance durations and
unexecuted matrix cells are recorded in IMPLEMENTATION.md.
