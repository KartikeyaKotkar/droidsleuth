# Disposable private-worker fixtures

Build with `sh testdata/android/private-workers/build.sh NEW_OUTPUT_DIRECTORY`.
The build uses the same SDK platform and build tools as the
[main validation fixture](../README.md). It produces four APKs signed with
one disposable key. They request no permissions and keep no user data.

- `main.apk` installs as `io.github.droidsleuth.v03`. `FixtureActivity` binds
  the isolated service. `Remote` and `ServiceWorker` run in `:remote` and
  `:service`. `Global` runs under the same UID in the global process
  `io.github.droidsleuth.v03.global`.
- `other.apk` supplies a package-name lookalike under a different UID.
- `shareda.apk` and `sharedb.apk` declare the same shared UID. The lookup
  must show both packages when both are installed for the same user.

Use an approved disposable emulator with an explicitly selected serial.
Start `FixtureActivity`, then start the three exported services with the
`mode` string set to `idle`. Sending `exit` or `crash` to `Remote` stops
that worker. Start it again with `idle` after observing absence. Compare
the main and peer identities before and after each action using the process
list and scoped reads of `stat`, `status`, and `cmdline`.
