#!/usr/bin/env bash
#
# run_trecd.sh — start/stop trecd for manual testing
#
# Usage:
#   ./scripts/run_trecd.sh start [addr]
#   ./scripts/run_trecd.sh stop
#   ./scripts/run_trecd.sh restart [addr]
#
set -euo pipefail

ADDR="${2:-127.0.0.1:5061}"
PIDFILE="/tmp/trecd.pid"
LOGFILE="/tmp/trecd.log"

cmd_start() {
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "trecd already running (PID $(cat "$PIDFILE"))" >&2
        exit 1
    fi
    cd "$(dirname "$0")/.."
    go build -o trecd ./cmd/trecsd
    nohup ./trecd -addr "$ADDR" > "$LOGFILE" 2>&1 &
    echo $! > "$PIDFILE"
    sleep 1
    if kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "trecd started on ${ADDR} (PID $(cat "$PIDFILE"))"
    else
        echo "trecd failed to start" >&2
        cat "$LOGFILE" >&2
        exit 1
    fi
}

cmd_stop() {
    if [ ! -f "$PIDFILE" ]; then
        echo "no PID file found" >&2
        # try pkill as fallback
        pkill trecd 2>/dev/null || true
        exit 0
    fi
    pid=$(cat "$PIDFILE")
    kill "$pid" 2>/dev/null || true
    rm -f "$PIDFILE"
    echo "trecd stopped"
}

case "${1:-help}" in
    start)   cmd_start ;;
    stop)    cmd_stop  ;;
    restart) cmd_stop; sleep 1; cmd_start ;;
    *)
        echo "Usage: $0 {start|stop|restart} [addr]" >&2
        exit 1
        ;;
esac
