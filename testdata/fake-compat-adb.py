#!/usr/bin/env python3
"""Deterministic hostile ADB fixture. Never connects to an ADB server."""
import os
import pathlib
import sys

root = pathlib.Path(os.environ["DROIDSLEUTH_COMPAT_ROOT"])
args = sys.argv[1:]
if args[:1] == ["-s"]:
    args = args[2:]
if args[:1] != ["shell"]:
    sys.exit(90)
command = args[-1]


def counter(source):
    path = root / source
    value = int(path.read_text()) + 1 if path.exists() else 1
    path.write_text(str(value))
    return value


def event(second, index=0):
    return (f"{second}.123456 1000 900 900 I am_anr: "
            f"[0,101,com.example.app,0,synthetic report {index}]\n")


if "__DS_CLOCK__" in command:
    n = counter("snapshots")
    if n % 300 == 299:
        sys.exit(1)
    text = (root / "snapshot.txt").read_text()
    text = text.replace("1794836454", str(1700000000 + n))
    text = text.replace("1000 0 0", f"{1000 + (n // 300) * 100} 0 0")
    sys.stdout.write(text)
elif command == "cat /proc/sys/kernel/random/boot_id":
    print("11111111-1111-1111-1111-111111111111")
elif command == "getprop persist.sys.timezone":
    print("UTC")
elif command.startswith("dumpsys activity exit-info"):
    n = counter("history")
    if n % 7 == 2:
        print("Permission Denial")
    else:
        print("ACTIVITY MANAGER PROCESS EXIT INFO")
elif command.startswith("logcat"):
    n = counter("logcat")
    phase = n % 20
    if phase == 2:
        sys.exit(1)
    if phase == 3:
        # The real bounded ADB transport must reject partial oversized output.
        sys.stdout.write("x" * ((4 << 20) + 1))
    elif phase in (9, 10):
        # Replayed burst: overflow the 64-observation poll limit, then deduplicate.
        sys.stdout.write("".join(event(1700000000 + n - phase + 9, i) for i in range(80)))
    else:
        if phase == 5:
            print("logd: synthetic truncated record")
        sys.stdout.write(event(1700000000))
        print(f"1700000000.123457 10124 202 202 E AndroidRuntime: unrelated-private-sentinel-{n}")
else:
    sys.exit(91)
