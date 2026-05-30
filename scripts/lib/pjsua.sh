#!/usr/bin/env bash
#
# run_pjsua — run pjsua with controlled duration and hard timeout
#
# Provides a consistent way to invoke pjsua from shell scripts across
# macOS and Linux with output capture and timeout enforcement.
#
# Usage:
#   source scripts/lib/pjsua.sh
#   run_pjsua [--timeout <secs>] [--pidfile <path>] <duration> <log_file> [pjsua_args...]
#
# The function blocks until pjsua exits.  Run it in the background (&)
# for concurrent pjsua instances (e.g., Bob callee + Alice caller).
# The pidfile (default: <log_file>.pid) contains pjsua's PID so you
# can kill it directly from the caller.
#
# Options:
#   --timeout <secs>  Hard kill timeout (default: duration + 10, min 30)
#   --pidfile <path>  Write pjsua's PID here (default: <log_file>.pid)
#
# Positional arguments:
#   <duration>   Seconds to keep pjsua alive (fed as "sleep N" on stdin)
#   <log_file>   Capture stdout+stderr to this file
#   pjsua_args   Forwarded verbatim to pjsua
#
# Returns:
#   124 on timeout, otherwise pjsua's exit code

run_pjsua() {
    local timeout_secs=""
    local pidfile=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --timeout) timeout_secs="$2"; shift 2 ;;
            --pidfile) pidfile="$2";     shift 2 ;;
            --)        shift; break ;;
            *)         break ;;
        esac
    done

    if [ $# -lt 2 ]; then
        echo "usage: run_pjsua [--timeout <secs>] [--pidfile <path>] <duration> <log_file> [pjsua_args...]" >&2
        return 1
    fi

    local duration="$1"
    local logfile="$2"
    shift 2

    [ -z "$timeout_secs" ] && timeout_secs=$(( duration + 10 ))
    [ "$timeout_secs" -lt 30 ] && timeout_secs=30
    [ -z "$pidfile" ] && pidfile="${logfile}.pid"

    # Keep stdin open a bit past duration so pjsua can finish cleanly
    echo "DEBUG: pjsua_args are: '$*'" >&2
    (
        echo "sleep $duration"
        sleep $(( duration + 3 ))
    ) | pjsua --no-cli-console --null-audio "$@" > "$logfile" 2>&1 &
    local pjsua_pid=$!
    echo "$pjsua_pid" > "$pidfile"

    # Hard timeout watcher
    {
        sleep "$timeout_secs"
        kill "$pjsua_pid" 2>/dev/null
    } &
    local watcher_pid=$!

    wait "$pjsua_pid" 2>/dev/null || true
    local rc=$?

    kill "$watcher_pid" 2>/dev/null || true
    rm -f "$pidfile"

    # Killed by signal (e.g., timeout watcher) → 124
    if [ $rc -gt 128 ]; then
        rc=124
    fi
    return $rc
}
