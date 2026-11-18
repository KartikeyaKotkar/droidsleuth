#!/usr/bin/env python3
"""Disposable Android runtime validation, not a product command.

Review mutation commands before running. Never uses a phone, adb root, logcat
clearing or existing user AVDs. Keeps at most one task-owned extra SDK image.
"""
import argparse
import datetime
import hashlib
import json
import os
import pathlib
import re
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parents[1]
PACKAGE = "io.github.droidsleuth.validation"
SDK = pathlib.Path(os.environ.get("ANDROID_SDK_ROOT", str(pathlib.Path.home() / "Android/Sdk")))
MANAGER = SDK / "cmdline-tools/latest/bin/sdkmanager"
AVDMANAGER = SDK / "cmdline-tools/latest/bin/avdmanager"
ADB = SDK / "platform-tools/adb"
NOISE = 'i=0; while [ "$i" -lt 10000 ]; do log -p e -t AndroidRuntime "DroidSleuth unrelated validation noise $i"; i=$((i+1)); done'


def run(args, timeout=180, check=True):
    return subprocess.run([str(x) for x in args], cwd=ROOT, timeout=timeout,
                          check=check, text=True, capture_output=True)


def refs(image):
    result = []
    for entry in (pathlib.Path.home() / ".android/avd").glob("*.ini"):
        for line in entry.read_text().splitlines():
            if line.startswith("path="):
                config = pathlib.Path(line[5:]) / "config.ini"
                if config.exists() and image.rstrip("/") in config.read_text():
                    result.append(str(entry))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("api", type=int, choices=[30, 33, 35, 36, 37])
    parser.add_argument("--seconds", type=int, default=1800)
    args = parser.parse_args()
    if not 600 <= args.seconds <= 7200:
        parser.error("capture duration must be 600..7200 seconds")
    api = args.api
    serial = {30: "emulator-5572", 33: "emulator-5574", 35: "emulator-5576", 36: "emulator-5578", 37: "emulator-5570"}[api]
    name = f"droidsleuth-gate-api{api}"
    out = ROOT / f".validation/compat/api{api}-runtime"
    avd = out / "device.avd"
    if (pathlib.Path.home() / f".android/avd/{name}.ini").exists():
        raise RuntimeError("AVD name already exists; refusing replacement")
    if re.search(rf"^{re.escape(serial)}\s", run([ADB, "devices", "-l"]).stdout, re.M):
        raise RuntimeError("target serial already connected; refusing mutation")
    out.mkdir(parents=True, exist_ok=False)
    tag = "google_apis_playstore" if api == 37 else "default"
    level = "37.0" if api == 37 else str(api)
    image_relative = f"system-images/android-{level}/{tag}/x86_64/"
    image = SDK / image_relative
    package = f"system-images;android-{level};{tag};x86_64"
    owned_image = not image.exists()
    manifest = dict(api=api, serial=serial, evidence_class="emulator", status="starting", owned_image=owned_image, phases=[])
    def save():
        (out / "manifest.json").write_text(json.dumps(manifest, indent=2))
    def phase(label):
        manifest["phase"] = label
        manifest["phases"].append(dict(name=label, host_utc=datetime.datetime.now(datetime.timezone.utc).isoformat()))
        save()
        print(f"API {api}: {label}", flush=True)
    def adb(*parts, timeout=30, check=True):
        return run([ADB, "-s", serial, *parts], timeout=timeout, check=check)
    emulator = capture = broadcast = None
    avd_created = False
    installed = False
    try:
        phase("SDK image")
        if owned_image:
            result = run([MANAGER, package], timeout=900)
            (out / "sdk.txt").write_text(result.stdout + result.stderr)
        manifest["image_properties"] = (image / "source.properties").read_text()
        # A newly named task-owned AVD is required; never --force an existing one.
        result = run([AVDMANAGER, "create", "avd", "--name", name, "--path", avd, "--package", package, "--device", "pixel_2"])
        avd_created = True
        (out / "avd.txt").write_text(result.stdout + result.stderr)
        config = avd / "config.ini"
        text = config.read_text().replace("disk.dataPartition.size=6G", "disk.dataPartition.size=2G").replace("hw.sdCard=yes", "hw.sdCard=no").replace("hw.lcd.width=1080", "hw.lcd.width=480").replace("hw.lcd.height=1920", "hw.lcd.height=800")
        config.write_text(text)
        phase("boot")
        with (out / "emulator.txt").open("w") as log:
            emulator = subprocess.Popen([str(SDK / "emulator/emulator"), "-avd", name, "-port", serial.split("-")[1], "-no-window", "-no-audio", "-no-snapshot", "-gpu", "swiftshader", "-memory", "2048", "-cores", "2"], stdout=log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + 240
        while True:
            if emulator.poll() is not None:
                raise RuntimeError("emulator failed; see emulator.txt")
            boot = adb("shell", "getprop sys.boot_completed", timeout=5, check=False)
            if boot.returncode == 0 and boot.stdout.strip() == "1":
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("boot timeout")
            time.sleep(2)
        manifest["shell_identity"] = adb("shell", "id").stdout.strip()
        if not manifest["shell_identity"].startswith("uid=2000(shell)"):
            raise RuntimeError("ordinary shell UID required; no privileged collection")
        if adb("shell", "getprop ro.build.version.sdk").stdout.strip() != str(api):
            raise RuntimeError("SDK mismatch")
        if adb("shell", f"cmd package list packages --user 0 {PACKAGE}").stdout.strip():
            raise RuntimeError("fixture package already installed; refusing replacement")
        phase("fixture install")
        adb("install", "--user", "0", "/tmp/droidsleuth-compat-apk/disposable.apk")
        installed = True
        def start(action=None):
            parts = ["shell", "am", "start", "--user", "0", "-W", "-n", PACKAGE + "/.MainActivity"]
            if action:
                parts += ["--es", "action", action]
            adb(*parts)
        def stop():
            adb("shell", "am", "force-stop", "--user", "0", PACKAGE)
        binary = out / "droidsleuth"
        def source_hashes():
            files = [p for folder in ("cmd", "internal") for p in (ROOT/folder).rglob("*.go") if not p.name.endswith("_test.go")]
            files += [ROOT/"go.mod", ROOT/"go.sum"]
            return {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(files)}
        for attempt in range(3):
            before = source_hashes()
            run(["go", "build", "-o", binary, "./cmd/droidsleuth"])
            if before == source_hashes():
                manifest["source_sha256"] = before
                break
        else:
            raise RuntimeError("source changed repeatedly during build; cannot record stable provenance")
        manifest["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
        start()
        phase("capture")
        capture_started = time.monotonic()
        with (out / "capture.txt").open("w") as log:
            capture = subprocess.Popen([str(binary), "watch", "--device", serial, "--user", "0", "--out", str(out / "session.db"), "--duration", f"{args.seconds}s", PACKAGE], stdout=log, stderr=subprocess.STDOUT)
        time.sleep(5)
        phase("Java crash")
        stop(); time.sleep(3); start("java"); time.sleep(10)
        phase("native crash")
        stop(); time.sleep(3); start("native"); time.sleep(12)
        phase("ANR")
        start()
        with (out / "broadcast.txt").open("w") as log:
            broadcast = subprocess.Popen([str(ADB), "-s", serial, "shell", "am", "broadcast", "--user", "0", "--receiver-foreground", "-n", PACKAGE + "/.BlockReceiver", "-a", PACKAGE + ".BLOCK"], stdout=log, stderr=subprocess.STDOUT)
        time.sleep(60)
        # Collect only scoped fixture history; log bodies from other apps are not saved.
        manifest["exit_history_before_cleanup"] = adb("shell", "dumpsys activity exit-info " + PACKAGE).stdout
        stop()
        broadcast.wait(timeout=15)
        start()
        phase("unrelated log volume")
        noise = []
        for iteration in range(3):
            begin = time.monotonic(); result = adb("shell", NOISE, timeout=300)
            noise.append(dict(iteration=iteration, seconds=time.monotonic()-begin, records_attempted=10000, stderr=result.stderr))
        manifest["noise"] = noise
        phase("steady capture")
        while capture.poll() is None:
            if time.monotonic()-capture_started > args.seconds+30:
                raise RuntimeError("capture failed to stop by deadline")
            time.sleep(2)
        if capture.returncode:
            raise RuntimeError("capture failed; see capture.txt")
        phase("offline replay")
        env = dict(os.environ, PATH="/nonexistent")
        for command in ("replay", "investigate"):
            with (out / (command+".jsonl")).open("w") as log:
                subprocess.run([str(binary), command, "--json", str(out / "session.db")], check=True, timeout=60, env=env, stdout=log)
        manifest["status"] = "complete"
        phase("complete")
    except BaseException as exc:
        manifest["status"] = "failed"
        manifest["error"] = str(exc)
        save()
        raise
    finally:
        if capture is not None and capture.poll() is None:
            capture.terminate(); capture.wait(timeout=15)
        if installed:
            adb("uninstall", PACKAGE, check=False)
        if emulator is not None and emulator.poll() is None:
            adb("emu", "kill", check=False)
            emulator.wait(timeout=30)
        if avd_created:
            run([AVDMANAGER, "delete", "avd", "--name", name])
        if owned_image and not refs(image_relative):
            run([MANAGER, "--uninstall", package])
        manifest["cleanup_done"] = True
        save()


if __name__ == "__main__":
    main()
