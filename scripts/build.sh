#!/usr/bin/env bash
set -euo pipefail

VALID_TARGETS=("linux-amd64" "linux-arm64" "windows-amd64" "all")

usage() {
    cat <<'EOF'
Usage: scripts/build.sh [target] [options]

Build gotiler-core test artifacts with Docker and run tests when the current
host can execute the target platform.

Targets:
  linux-amd64      Build and run tests inside Docker
  linux-arm64      Cross-build Linux ARM64 test binaries, run on Linux ARM64
  windows-amd64    Cross-build Windows AMD64 test binaries, run on Windows AMD64
  all              Run every target

Options:
  --no-cache       Pass --no-cache to docker build
  --skip-run       Build artifacts only; do not execute generated test binaries
  -h, --help       Show this help
EOF
}

TARGET="all"
NO_CACHE=()
SKIP_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        linux-amd64|linux-arm64|windows-amd64|all)
            TARGET="$1"
            ;;
        --no-cache)
            NO_CACHE=(--no-cache)
            ;;
        --skip-run)
            SKIP_RUN=true
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
    shift
done

is_valid_target() {
    local target="$1"
    local valid
    for valid in "${VALID_TARGETS[@]}"; do
        [[ "$target" == "$valid" ]] && return 0
    done
    return 1
}

if ! is_valid_target "$TARGET"; then
    echo "Invalid target: $TARGET" >&2
    usage >&2
    exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$host_os" in
    mingw*|msys*|cygwin*) host_os="windows" ;;
    linux*) host_os="linux" ;;
    darwin*) host_os="darwin" ;;
esac

host_arch="$(uname -m | tr '[:upper:]' '[:lower:]')"
case "$host_arch" in
    x86_64|amd64) host_arch="amd64" ;;
    aarch64|arm64) host_arch="arm64" ;;
esac

native_path() {
    local path="$1"
    if [[ "$host_os" == "windows" ]] && command -v cygpath >/dev/null 2>&1; then
        cygpath -w "$path"
    else
        printf '%s\n' "$path"
    fi
}

can_run_target() {
    local target="$1"
    case "$target" in
        linux-amd64) [[ "$host_os" == "linux" && "$host_arch" == "amd64" ]] ;;
        linux-arm64) [[ "$host_os" == "linux" && "$host_arch" == "arm64" ]] ;;
        windows-amd64) [[ "$host_os" == "windows" && "$host_arch" == "amd64" ]] ;;
        *) return 1 ;;
    esac
}

run_test_binaries() {
    local target="$1"
    local test_dir="build/tests/$target"
    local proj_dir
    proj_dir="$(native_path "$repo_root/build/share/$target")"

    if [[ ! -d "$test_dir" ]]; then
        echo "No test binaries found for $target at $test_dir" >&2
        return 1
    fi

    local found=false
    local test_file
    for test_file in "$test_dir"/*; do
        [[ -f "$test_file" ]] || continue
        found=true
        chmod +x "$test_file" 2>/dev/null || true
        echo "Running $(basename "$test_file")..."
        PROJ_DATA="$proj_dir" "$test_file" -test.v
    done

    if [[ "$found" != true ]]; then
        echo "No runnable test binaries found for $target at $test_dir" >&2
        return 1
    fi
}

build_linux_amd64() {
    echo "==> Building and testing linux-amd64 in Docker"
    docker build "${NO_CACHE[@]}" --target linux-amd64-test .
}

build_artifacts() {
    local target="$1"
    local docker_target="$2"
    local out_dir="build/$target"

    echo "==> Building $target test artifacts in Docker"
    rm -rf "$out_dir"
    mkdir -p "$out_dir"
    docker build "${NO_CACHE[@]}" --target "$docker_target" --output "type=local,dest=$out_dir" .

    mkdir -p build/tests build/share
    rm -rf "build/tests/$target" "build/share/$target"
    mv "$out_dir/tests/$target" "build/tests/$target"
    mv "$out_dir/share/$target" "build/share/$target"
    rm -rf "$out_dir/tests" "$out_dir/share"

    if [[ "$SKIP_RUN" == true ]]; then
        echo "Skipping $target tests (--skip-run)"
    elif can_run_target "$target"; then
        run_test_binaries "$target"
    else
        echo "Skipping $target tests (host=${host_os}/${host_arch})"
    fi
}

run_target() {
    case "$1" in
        linux-amd64)
            build_linux_amd64
            ;;
        linux-arm64)
            build_artifacts linux-arm64 linux-arm64-artifacts
            ;;
        windows-amd64)
            build_artifacts windows-amd64 windows-amd64-artifacts
            ;;
    esac
}

if [[ "$TARGET" == "all" ]]; then
    for target in linux-amd64 linux-arm64 windows-amd64; do
        run_target "$target"
    done
else
    run_target "$TARGET"
fi

echo "Done."
