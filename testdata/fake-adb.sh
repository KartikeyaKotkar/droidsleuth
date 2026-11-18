#!/bin/sh
# Fixture-driven subprocess, never talks to an ADB server or Android device.
set -eu
root=${DROIDSLEUTH_FIXTURES:?}
if [ "$1" = '-s' ]; then
  [ "$2" = 'fixture-phone' ] || exit 90
  shift 2
fi
case "$1" in
  devices)
    printf 'List of devices attached\nfixture-phone device model:fixture\n'
    exit 0
    ;;
  shell)
    [ "$2" = '-T' ] || exit 91
    case "$3" in
       'getprop ro.build.version.sdk') printf '%s\n' "${DROIDSLEUTH_FIXTURE_SDK:-30}"; exit 0;;
      'am get-current-user') printf '0\n'; exit 0;;
      'cmd package list packages -U --user 0 com.example.app')
        printf 'package:com.example.app uid:10123\n'; exit 0;;
    esac
    ;;
  *) exit 92;;
esac
n=1
if [ -f "$root/count" ]; then n=$(cat "$root/count"); fi
printf '%s\n' "$((n + 1))" > "$root/count"
if [ -f "$root/$n.block" ]; then
  printf '%s\n' "$$" > "$root/pid"
  exec sleep 30
fi
if [ -f "$root/$n.stderr" ]; then cat "$root/$n.stderr" >&2; fi
if [ -f "$root/$n.stdout" ]; then cat "$root/$n.stdout"; else exit 93; fi
if [ -f "$root/$n.exit" ]; then exit "$(cat "$root/$n.exit")"; fi
