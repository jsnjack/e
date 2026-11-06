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

# A bus that starts the document portal while sharing /run/user/$UID unmounts
# the desktop's doc filesystem, and Flatpak apps then fail to start. The tests
# get their own runtime directory, so a portal started on their private bus
# (WebKit needs one) mounts there, and the script fails if the desktop's mount
# still disappears.
desktop_doc=/run/user/$(id -u)/doc
had_doc=
if mountpoint -q "$desktop_doc" 2>/dev/null; then
	had_doc=1
fi

runtime_dir=$(mktemp -d)
socket="e-test-$$"
log="$runtime_dir/weston.log"
cleanup() {
	local status=$?
	if [[ -n "${weston_pid:-}" ]]; then
		kill "$weston_pid" 2>/dev/null || true
		wait "$weston_pid" 2>/dev/null || true
	fi
	# Services started on the private bus exit after it does; the document
	# portal among them unmounts the private doc filesystem on its way out.
	for _ in $(seq 50); do
		mountpoint -q "$runtime_dir/doc" 2>/dev/null || break
		sleep 0.1
	done
	if mountpoint -q "$runtime_dir/doc" 2>/dev/null; then
		fusermount3 -u -z "$runtime_dir/doc" 2>/dev/null || true
	fi
	rm -rf --one-file-system "$runtime_dir" 2>/dev/null || true
	if [[ -n $had_doc ]] && ! mountpoint -q "$desktop_doc"; then
		echo "$desktop_doc was unmounted during this run, so Flatpak apps cannot start." >&2
		echo "Restore it with: systemctl --user restart xdg-document-portal.service" >&2
		status=1
	fi
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

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

# Unset DISPLAY so GTK cannot fall back to the desktop's X server. The tests
# get a private session bus, so they cannot reach the desktop's notifications,
# settings or running E instance.
env -u DISPLAY -u WAYLAND_SOCKET XDG_RUNTIME_DIR="$runtime_dir" WAYLAND_DISPLAY="$socket" \
	GDK_BACKEND=wayland GSETTINGS_BACKEND=memory GIO_USE_VFS=local ADW_DISABLE_PORTAL=1 \
	GTK_A11Y=none NO_AT_BRIDGE=1 \
	dbus-run-session -- "$@"
