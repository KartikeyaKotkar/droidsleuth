# Disposable lifecycle validation application

This fixture exists only to validate DroidSleuth against a process without user
data. It has one Activity with idle and validation workload modes, no requested permissions, no services, and no
application persistence. Its package is `io.github.droidsleuth.validation`.

Build locally with JDK tools, Android SDK platform 36 and build-tools 36.0.0:

```sh
sh scripts/build-validation-apk.sh /tmp/droidsleuth-new-validation-build
```

The output directory must not exist. Set `ANDROID_SDK_ROOT` if the SDK is not
under `~/Android/Sdk`. The script compiles against the installed Android SDK,
creates a throwaway signing key and verifies the resulting APK. It does not run
ADB or modify a device. APKs, keys and captured sessions stay outside the repo.

Obtain explicit approval before installation, launch, force-stop or uninstall.
Install without a replacement flag so an existing package cannot be overwritten.
Use an explicit device serial and Android user. A physical USB-disconnection
test requires a person to unplug and reconnect the cable; resetting an ADB
transport is not evidence of a physical USB test.


The `resources` action touches a 32 MiB direct buffer after four seconds and
retains it for the activity's lifetime. It starts one CPU worker at eight
seconds and two workers at 22 seconds. Each phase lasts 12 seconds. Use a fresh
activity launch so these phases are scheduled once.
