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

    # 1. Argument Parsing
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

    # Set defaults
    [ -z "$timeout_secs" ] && timeout_secs=$(( duration + 10 ))
    [ "$timeout_secs" -lt 30 ] && timeout_secs=30
    [ -z "$pidfile" ] && pidfile="${logfile}.pid"

    # 2. Setup FIFO (Named Pipe)
    local fifo="${logfile}.fifo"
    rm -f "$fifo"
    mkfifo "$fifo"

    # 3. The "Keeper"
    # This holds the FIFO open for writing so pjsua doesn't close on start.
    # We use a background loop that we can kill later.
    ( while true; do sleep 100; done ) > "$fifo" &
    local keeper_pid=$!

    # 4. Start PJSUA
    # It reads from the FIFO. We use "$@" to pass all remaining PJSIP flags.
    # The < /dev/null is NOT used here because we want it to read from the FIFO.
    pjsua --no-cli-console --null-audio "$@" < "$fifo" > "$logfile" 2>&1 &
    local pjsua_pid=$!
    echo "$pjsua_pid" > "$pidfile"

    # 5. Controller Logic (Quit & Cleanup)
    {
        sleep "$duration"
        # Send the quit command into the FIFO
        echo "quit" > "$fifo" 2>/dev/null
        
        # Give it a moment to exit gracefully before killing the keeper
        sleep 2
        kill "$keeper_pid" 2>/dev/null
        rm -f "$fifo"
    } &
    local controller_pid=$!

    # 6. Hard Timeout Watcher (Safety Net)
    {
        sleep "$timeout_secs"
        if kill -0 "$pjsua_pid" 2>/dev/null; then
            echo "Timeout reached. Killing pjsua..." >&2
            kill -9 "$pjsua_pid" 2>/dev/null
        fi
    } &
    local watcher_pid=$!

    # 7. Wait for pjsua to finish
    wait "$pjsua_pid" 2>/dev/null || true
    local rc=$?

    # Cleanup all background helpers
    kill "$watcher_pid" "$controller_pid" "$keeper_pid" 2>/dev/null || true
    rm -f "$pidfile" "$fifo"

    # Normalize exit code
    [ $rc -gt 128 ] && rc=124
    return $rc
}
