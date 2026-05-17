#!/usr/bin/env bash
#
# test_options.sh — send periodic SIP OPTIONS requests and verify responses
#
# Validates: 200 OK with Allow header including REGISTER.
#
# Prerequisites:
#   - trecd running (see scripts/run_trecd.sh)
#   - sipsak installed (brew install sipsak)
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-t target] [-c count] [-i interval]

Send periodic SIP OPTIONS and validate responses.

Options:
  -t target   Server address (default: 127.0.0.1:5061)
  -c count    Number of OPTIONS requests to send (default: 5)
  -i interval Seconds between requests (default: 2)
  -h          Show this help and exit
EOF
    exit 0
}

TARGET="127.0.0.1:5061"
COUNT=5
INTERVAL=2
while getopts ":hc:t:i:" opt; do
    case "$opt" in
        h) usage ;;
        t) TARGET="$OPTARG" ;;
        c) COUNT="$OPTARG" ;;
        i) INTERVAL="$OPTARG" ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage ;;
    esac
done
shift $((OPTIND - 1))

PASS=0
FAIL=0

pass() { echo "  ✓ $1"; ((PASS++)); }
fail() { echo "  ✗ $1"; ((FAIL++)); }

echo "=== OPTIONS validation ($TARGET, ${COUNT} requests, ${INTERVAL}s interval) ==="
echo ""

for i in $(seq 1 "$COUNT"); do
    echo "--- request $i/$COUNT ---"
    log=$(sipsak -s "sip:${TARGET}" -vv 2>&1 || true)

    if echo "$log" | grep -q "200 OK"; then
        pass "OPTIONS $i — 200 OK"
    else
        fail "OPTIONS $i — missing 200 OK"
    fi

    if echo "$log" | grep -q "Allow:"; then
        pass "OPTIONS $i — Allow header present"
    else
        fail "OPTIONS $i — missing Allow header"
    fi

    if echo "$log" | grep -qi "REGISTER"; then
        pass "OPTIONS $i — Allow includes REGISTER"
    else
        fail "OPTIONS $i — Allow missing REGISTER"
    fi

    if [ "$i" -lt "$COUNT" ]; then
        sleep "$INTERVAL"
    fi
done

echo ""
echo "=== results: ${PASS} passed, ${FAIL} failed ==="
exit $FAIL
