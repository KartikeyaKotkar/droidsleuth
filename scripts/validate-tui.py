#!/usr/bin/env python3
"""Exercise the offline investigation TUI through a Linux pseudoterminal."""

import argparse
import fcntl
import hashlib
import json
import os
import pathlib
import pty
import select
import signal
import struct
import subprocess
import tempfile
import termios
import time


class Terminal:
    def __init__(self, executable, arguments, env):
        self.master, self.slave = pty.openpty()
        self.size(28, 110)
        self.original = termios.tcgetattr(self.slave)
        self.child = subprocess.Popen(
            [executable, "investigate", "--tui", *arguments],
            stdin=self.slave,
            stdout=self.slave,
            stderr=self.slave,
            env=env,
        )

    def size(self, rows, columns):
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))
        if hasattr(self, "child"):
            # The test PTY is not the subprocess's controlling terminal.
            self.child.send_signal(signal.SIGWINCH)

    def send(self, keys):
        os.write(self.master, keys)

    def expect(self, text, timeout=10):
        token = text.encode()
        end = time.monotonic() + timeout
        observed = bytearray()
        while token not in observed and time.monotonic() < end:
            if select.select([self.master], [], [], max(0, end - time.monotonic()))[0]:
                observed.extend(os.read(self.master, 65536))
        if token not in observed:
            raise AssertionError(f"missing {text!r} in terminal output {bytes(observed[-4096:])!r}")
        return bytes(observed)

    def restored(self, status=0):
        self.expect("\x1b[?1049l")
        assert self.child.wait(timeout=5) == status
        assert termios.tcgetattr(self.slave) == self.original, "terminal attributes not restored"

    def __enter__(self):
        return self

    def __exit__(self, *_):
        if self.child.poll() is None:
            self.child.kill()
            self.child.wait()
        os.close(self.master)
        os.close(self.slave)


def fingerprint(path):
    info = path.stat()
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(64 << 10):
            digest.update(chunk)
    return digest.digest(), info.st_size, info.st_mtime_ns


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("recording", type=pathlib.Path, help="finalized recording with a diagnostic anchor")
    args = parser.parse_args()
    binary = args.binary.resolve()
    recording = args.recording.resolve()
    initial = fingerprint(recording)
    env = dict(os.environ, PATH="/nonexistent", TERM="xterm-256color")
    cli = [binary, "investigate", "--timeline", "--json"]
    index = subprocess.check_output([*cli, recording], env=env)
    assert index == subprocess.check_output([*cli, recording], env=env)
    timeline = json.loads(index)
    entry = next(row for row in timeline["incidents"] if row["id"].startswith("E"))
    incident = entry["id"]
    detail_args = ["--incident", incident, recording]
    detail = subprocess.check_output([*cli, *detail_args], env=env)
    assert detail == subprocess.check_output([*cli, *detail_args], env=env)
    reference = json.loads(detail)["incident"]["report"]["reference"]
    ref_label = f"Observation #{reference['sequence']} · lines {reference['first_line']}–{reference['last_line']}"

    with Terminal(binary, [recording], env) as terminal:
        terminal.expect("· schema ")
        terminal.send(b"g")
        terminal.expect("Open incident by ID")
        terminal.send(incident.encode() + b"\r")
        terminal.expect("association unproven")
        terminal.send(b"\r")
        terminal.expect("Device occurrence")
        terminal.send(b"e")
        terminal.expect(ref_label)
        terminal.send(b"\r")
        terminal.expect("Line numbers refer to the recorded source.")
        terminal.size(5, 20)
        terminal.expect("q quit · resize")
        terminal.size(28, 110)
        terminal.expect("Stored evidence")
        terminal.send(b"\x1b")
        terminal.expect("Evidence references")
        terminal.send(b"q")
        terminal.restored()

    with Terminal(binary, detail_args, env) as terminal:
        terminal.expect("association unproven")
        terminal.send(b"\x03")
        terminal.restored()

    with Terminal(binary, detail_args, env) as terminal:
        terminal.expect("association unproven")
        terminal.child.send_signal(signal.SIGTERM)
        terminal.restored()

    with Terminal(binary, detail_args, env) as terminal:
        terminal.expect("association unproven")
        terminal.send(b"\x04")
        terminal.restored()

    # Private-worker fixtures keep the main process alive. Show the observed
    # main identity at both bounds of a selected worker disappearance.
    if timeline["metadata"]["schema"] == 4:
        worker = next(row for row in timeline["incidents"] if row["id"].startswith("L"))
        with Terminal(binary, ["--incident", worker["id"], recording], env) as terminal:
            terminal.expect("Timeline ·")
            terminal.send(b"\r")
            frame = terminal.expect("Main process observed")
            assert b"Observed disappearance. Cause unknown." in frame
            assert worker["process"].encode() in frame
            terminal.send(b"q")
            terminal.restored()

    with tempfile.TemporaryDirectory(prefix="droidsleuth-tui-") as root:
        directory = pathlib.Path(root) / "droidsleuth"
        directory.mkdir()
        managed = directory / "20260101T000000.000000000.db"
        # --last ignores symlinks. Copy only the selected recording.
        with recording.open("rb") as src, managed.open("wb") as dst:
            while chunk := src.read(64 << 10):
                dst.write(chunk)
        with Terminal(binary, ["--last"], dict(env, XDG_STATE_HOME=root)) as terminal:
            terminal.expect("· schema ")
            terminal.send(b"\x1b[B")
            terminal.send(b"\r")
            terminal.expect("Timeline ·")
            terminal.send(b"q")
            terminal.restored()
        corrupt = directory / "invalid.db"
        corrupt.write_bytes(b"not a recording")
        with Terminal(binary, ["--incident", incident, corrupt], env) as terminal:
            terminal.restored(status=1)

    assert fingerprint(recording) == initial, "input recording changed"
    print(f"Linux PTY interaction and restoration passed for {recording.name}")


if __name__ == "__main__":
    main()
