#!/usr/bin/env bash
# Produce the Linux amd64 release archive from a clean local commit.
set -euo pipefail
umask 022
export LC_ALL=C

if [[ $# != 1 ]]; then
    printf 'Usage: %s NEW_OUTPUT_DIRECTORY\n' "$0" >&2
    exit 2
fi
output=$(realpath -m -- "$1")
repo=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
if [[ -e "$output" || -L "$output" ]]; then
    printf 'Output directory already exists: %s\n' "$output" >&2
    exit 1
fi
if [[ -n $(git -C "$repo" status --porcelain) ]]; then
    printf 'Package from a clean checkout.\n' >&2
    exit 1
fi
revision=$(git -C "$repo" rev-parse HEAD)
epoch=$(git -C "$repo" show -s --format=%ct "$revision")
version=v0.2.0
name="droidsleuth-$version-linux-amd64"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
trap 'exit 1' HUP INT TERM

mkdir "$work/source" "$work/$name"
git -C "$repo" archive "$revision" | tar -x -C "$work/source"
bundle="$work/$name"

(
    cd "$work/source"
    export GOTOOLCHAIN=go1.23.12 GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0
    export GOENV=off GOFLAGS='' GOWORK=off
    unset GOEXPERIMENT
    go mod verify
    go build -mod=readonly -trimpath -buildvcs=false -o "$bundle/droidsleuth" ./cmd/droidsleuth
    go list -mod=readonly -buildvcs=false -deps -f '{{if and .Module (not .Module.Main)}}{{.Module.Path}}{{"\t"}}{{.Module.Version}}{{"\t"}}{{.Module.Dir}}{{end}}' ./cmd/droidsleuth |
        sed '/^$/d' | sort -u > "$work/modules"

    toolchain=$(go env GOROOT)
    {
        printf 'Go toolchain\n'
        cat "$toolchain/LICENSE" "$toolchain/PATENTS"
        while IFS=$'\t' read -r module module_version module_dir; do
            find "$module_dir" -maxdepth 1 -type f \( -iname '*license*' -o -iname 'copying*' -o -iname 'notice*' -o -iname copyright -o -name PATENTS \) |
                sort > "$work/notices"
            if [[ ! -s "$work/notices" ]]; then
                printf 'Missing dependency notice for %s %s\n' "$module" "$module_version" >&2
                exit 1
            fi
            while IFS= read -r notice; do
                printf '\n\n%s %s (%s)\n\n' "$module" "$module_version" "${notice##*/}"
                cat "$notice"
            done < "$work/notices"
        done < "$work/modules"
    } > "$bundle/THIRD_PARTY_NOTICES"
    printf 'Version: %s\nSource: %s\nGo: %s\nTarget: linux/amd64 (GOAMD64=v1, CGO disabled)\n' \
        "$version" "$revision" "$(go env GOVERSION)" > "$bundle/BUILD_INFO"
)
install -m 0644 "$work/source/README.md" "$work/source/LICENSE" "$work/source/RELEASE_NOTES.md" "$bundle/"
chmod 0644 "$bundle/BUILD_INFO" "$bundle/THIRD_PARTY_NOTICES"
chmod 0755 "$bundle" "$bundle/droidsleuth"

tar -C "$work" --format=gnu --sort=name --mtime="@$epoch" --owner=0 --group=0 --numeric-owner -cf "$work/$name.tar" "$name"
gzip -n "$work/$name.tar"
(cd "$work" && sha256sum "$name.tar.gz" > SHA256SUMS)
mkdir -- "$output"
install -m 0644 "$work/$name.tar.gz" "$work/SHA256SUMS" "$output/"
printf '%s\n' "$output/$name.tar.gz" "$output/SHA256SUMS"
