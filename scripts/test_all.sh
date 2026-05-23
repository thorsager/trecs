#!/usr/bin/env bash
#
# test_all.sh — run registration + periodic-options validation
#
# Starts trecd, runs both test suites, stops trecd.
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-t target]

Start trecd, run REGISTER and OPTIONS validation, stop trecd.

Options:
  -t target   Server address (default: 127.0.0.1:5061)
  -h          Show this help and exit
EOF
    exit 0
}

TARGET="127.0.0.1:5061"
while getopts ":ht:" opt; do
    case "$opt" in
        h) usage ;;
        t) TARGET="$OPTARG" ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage ;;
    esac
done
shift $((OPTIND - 1))

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EXIT=0

DIALPLAN_FILE=$(mktemp /tmp/trec_test_dialplan.XXXXXX.json)
cat > "$DIALPLAN_FILE" <<JSON
{
  "extensions": {
    "echo": { "action": "echo" },
    "play": { "action": "play", "file": "$ROOT/testdata/EDIS-SCD-02.wav" }
  }
}
JSON

cleanup() {
    echo ""
    echo "=== stopping trecd ==="
    if [ -n "${TRECD_PID:-}" ] && kill -0 "$TRECD_PID" 2>/dev/null; then
        kill "$TRECD_PID" 2>/dev/null || true
        wait "$TRECD_PID" 2>/dev/null || true
    fi
    rm -f "$DIALPLAN_FILE"
    rm -f /tmp/trecd_test_all
    rm -f /tmp/trecd_server.log
}
trap cleanup EXIT

# Show pjsua logs on failure
show_pjsua_logs() {
    if [ "$EXIT" -gt 0 ]; then
        echo ""
        echo "=== pjsua diagnostic logs ==="
        for f in /tmp/trec_b2bua_test.*/*.log /tmp/trec_dp_echo.*/*.log /tmp/trec_dp_play.*/*.log; do
            if [ -f "$f" ]; then
                echo "--- $f ---"
                head -80 "$f" 2>/dev/null || echo "(empty)"
                echo ""
            fi
        done
    fi
}
trap 'show_pjsua_logs; cleanup' EXIT

echo "--- building trecd ---"
go build -o /tmp/trecd_test_all "$ROOT/cmd/trecsd/" 2>&1
echo "--- starting trecd on $TARGET with dialplan ---"
/tmp/trecd_test_all -addr "$TARGET" -dialplan "$DIALPLAN_FILE" 2>/tmp/trecd_server.log &
TRECD_PID=$!
sleep 2
if ! kill -0 "$TRECD_PID" 2>/dev/null; then
    echo "trecd failed to start" >&2
    exit 1
fi
echo "trecd started on $TARGET (PID $TRECD_PID)"

echo ""
echo "=================================================================="
echo " 1/4 — REGISTER validation via UDP"
echo "=================================================================="
"$ROOT/scripts/test_register.sh" -t "$TARGET" -p udp || ((EXIT++))

echo ""
echo "=================================================================="
echo " 2/4 — REGISTER validation via TCP"
echo "=================================================================="
"$ROOT/scripts/test_register.sh" -t "$TARGET" -p tcp || ((EXIT++))

echo ""
echo "=================================================================="
echo " 3/4 — OPTIONS validation via UDP (5 requests, 2s interval)"
echo "=================================================================="
"$ROOT/scripts/test_options.sh" -t "$TARGET" -c 5 -i 2 -p udp || ((EXIT++))

echo ""
echo "=================================================================="
echo " 4/4 — OPTIONS validation via TCP (5 requests, 2s interval)"
echo "=================================================================="
"$ROOT/scripts/test_options.sh" -t "$TARGET" -c 5 -i 2 -p tcp || ((EXIT++))

echo ""
echo "=================================================================="
echo "     — B2BUA edge-case tests"
echo "=================================================================="
"$ROOT/scripts/test_b2bua_full.sh" -t "$TARGET" || ((EXIT++))

echo ""
echo "=================================================================="
echo " 5/8 — Dialplan echo test (UDP)"
echo "=================================================================="
"$ROOT/scripts/test_dialpdan_echo.sh" -t "$TARGET" -p udp || ((EXIT++))

echo ""
echo "=================================================================="
echo " 6/8 — Dialplan echo test (TCP)"
echo "=================================================================="
"$ROOT/scripts/test_dialplan_echo.sh" -t "$TARGET" -p tcp || ((EXIT++))

echo ""
echo "=================================================================="
echo " 7/8 — Dialplan file playback test (UDP)"
echo "=================================================================="
"$ROOT/scripts/test_dialplan_play.sh" -t "$TARGET" -p udp || ((EXIT++))

echo ""
echo "=================================================================="
echo " 8/8 — Dialplan file playback test (TCP)"
echo "=================================================================="
"$ROOT/scripts/test_dialplan_play.sh" -t "$TARGET" -p tcp || ((EXIT++))

echo ""
echo "=================================================================="
if [ "$EXIT" -eq 0 ]; then
    echo " ALL TESTS PASSED"
else
    echo " ${EXIT} test suite(s) failed"
fi
echo "=================================================================="
exit $EXIT
