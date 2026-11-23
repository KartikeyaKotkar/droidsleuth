# Contributing to DroidSleuth

Use Linux and Go 1.23 or newer. A device is optional for the standard checks.
For changes involving ADB, start with deterministic fixtures and use a
disposable app for any controlled device test.

## Build and verify

From the repository root:

```sh
go build -o droidsleuth ./cmd/droidsleuth
go mod verify
go test ./...
go test -race ./...
go vet ./...
```

The Linux CI workflow runs the four verification commands on Go 1.23.12.
To smoke-test the interactive interface, run the following with a finalized
recording containing a diagnostic anchor:

```sh
python3 scripts/validate-tui.py ./droidsleuth recording.db
```

The CLI lives in `cmd/droidsleuth`. Internal packages cover ADB transport,
capture, evidence parsing, SQLite sessions, investigation, and the TUI.
Read the [implementation record](IMPLEMENTATION.md) for invariants and
[PLAN.md](PLAN.md) for project direction.

## Report an issue or open a PR

- Start with a reproducible bug or Android compatibility issue. Include the
  DroidSleuth version, exact command with private values removed, relevant
  platform details, expected behavior, and observed behavior.
- Keep changes focused. Explain the user-visible impact, the evidence for the
  change, and the tests you ran. Include a focused regression when changing a
  parser or collector. Document any compatibility or performance impact.
- Keep source reports, lifecycle observations, and inferred associations
  distinct. Preserve process identity and coverage gaps. A disappeared
  process retains an unknown termination cause. Present any source claim
  with its evidence and association status.
- Label the provenance of results as fixture, emulator, or physical device.
  Keep sanitized fixtures minimal and preserve the field order or other shape
  required to reproduce an issue. Remove serials, personal data, proprietary
  stack traces, and unrelated app output. Share selected, sanitized excerpts in
  place of private recordings, full logcat dumps, or bug reports.

By contributing you agree to license your changes under the
[MIT License](LICENSE).
