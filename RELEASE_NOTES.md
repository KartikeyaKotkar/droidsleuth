# DroidSleuth v0.1.0-rc1

DroidSleuth records Android main-process observations, scoped crash and ANR
reports, available exit history, and diagnostic coverage gaps. The Linux CLI
and terminal browser can inspect finalized SQLite recordings offline.
`watch --summary` prints incident IDs and exact-file commands when capture ends.
Optional resources include per-process CPU counters and approximate RSS.

The release candidate fixes a reproduced API 37 failure where a busy merged
numeric tail omitted a Java fatal stack still available in the crash buffer.
Numeric polls now read a separate bounded crash tail in the existing ADB call.
Process identity checks and incident uncertainty continue to apply. A
disappearance records an observation with an unknown cause.

## Supported and tested

- Binary target is Linux amd64. Source builds use Go 1.23 or later. Local
  candidate archives use Go 1.23.12, with source and build details in
  `BUILD_INFO`.
- Live capture uses Android SDK Platform-Tools, an authorized Android API 30
  or later device, and accessible process identity data. The release gate
  exercised API 30 AOSP and API 37 Google Play emulators. Earlier validation
  covered API 33, 35 and 36 emulators and one historical API 37 phone.
- Offline replay and investigation accept schemas 1–3. Plain capture writes
  schema 2, and `watch --resources` writes schema 3.

## Known limits

Android log buffers are finite. Filtered reads can miss events even when the
read succeeds, and OEM diagnostic formats and access vary. Exit history may
be missing or delayed. Android source reports can remain unbound to an exact
kernel process instance. Secondary, custom-named, and isolated processes are
outside main-process lifecycle tracking. Recording files and their exports
can contain sensitive app diagnostics.

See the README for installation, troubleshooting, and investigation. The
repository's implementation record contains the release-gate evidence and
performance measurements.
