#!/usr/bin/env bash
# Runs a command in a private, invisible Wayland session, so GTK tests never
# open windows on the user's desktop. Set E_TEST_VISIBLE=1 to use the current
# session instead.
set -euo pipefail

if [[ "${E_TEST_VISIBLE:-}" == 1 ]]; then
	exec "$@"
fi

if ! command -v weston >/dev/null 2>&1; then
	echo "weston is not installed. Install it with: sudo dnf install weston" >&2
	exit 1
fi

runtime_dir=$(mktemp -d)
socket="e-test-$$"
log="$runtime_dir/weston.log"
cleanup() {
	if [[ -n "${weston_pid:-}" ]]; then
		kill "$weston_pid" 2>/dev/null || true
		wait "$weston_pid" 2>/dev/null || true
	fi
	rm -rf "$runtime_dir"
}
trap cleanup EXIT

XDG_RUNTIME_DIR="$runtime_dir" weston --backend=headless --socket="$socket" \
	--width=1600 --height=1000 --idle-time=0 >"$log" 2>&1 &
weston_pid=$!

for _ in $(seq 100); do
	[[ -S "$runtime_dir/$socket" ]] && break
	if ! kill -0 "$weston_pid" 2>/dev/null; then
		cat "$log" >&2
		exit 1
	fi
	sleep 0.05
done
if [[ ! -S "$runtime_dir/$socket" ]]; then
	echo "headless weston did not start" >&2
	cat "$log" >&2
	exit 1
fi

# Unset DISPLAY so GTK cannot fall back to the desktop's X server. The D-Bus
# session stays, because the tests need no portal or keyring.
env -u DISPLAY XDG_RUNTIME_DIR="$runtime_dir" WAYLAND_DISPLAY="$socket" GDK_BACKEND=wayland "$@"
