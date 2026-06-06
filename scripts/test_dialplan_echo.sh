#!/usr/bin/env bash
#
# test_dialplan_echo.sh — end-to-end dialplan echo test
#
# Loads trecd with a dialplan that routes "echo" → echo action,
# calls the echo service via pjsua, and verifies audio echo.
#
# Prerequisites:
#   - trecd built (see scripts/run_trecd.sh) or use -s
#   - sox installed (brew install sox)
#   - pjsua installed (brew install pjsua-ua)
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-s] [-t target] [-d duration] [-p proto]

Dialplan echo test: call sip:echo@... via dialplan routing.

Options:
  -s          Auto-start trecd before the test
  -t target   Server address (default: 127.0.0.1:5061)
  -d duration Call duration in seconds (default: 4)
  -p proto    SIP transport: udp or tcp (default: udp)
  -h          Show this help and exit
EOF
    exit 0
}

AUTO_START=0
TARGET="127.0.0.1:5061"
DURATION=4
PROTO="udp"

while getopts ":hst:d:p:" opt; do
    case "$opt" in
        h) usage ;;
        s) AUTO_START=1 ;;
        t) TARGET="$OPTARG" ;;
        d) DURATION="$OPTARG" ;;
        p) PROTO="$OPTARG" ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage ;;
    esac
done
case "$PROTO" in
    udp|tcp) ;;
    *) echo "invalid protocol: $PROTO (use udp or tcp)" >&2; exit 1 ;;
esac
shift $((OPTIND - 1))

PASS=0
FAIL=0

pass() { echo "  ✓ $1"; ((PASS++)); }
fail() { echo "  ✗ $1"; ((FAIL++)); }

file_size() { stat -c %s "$1" 2>/dev/null || stat -f%z "$1" 2>/dev/null; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMPDIR=$(mktemp -d /tmp/trec_dp_echo.XXXXXX) || { echo "FAIL: mktemp"; exit 1; }

cleanup() {
    rm -rf "$TMPDIR"
    if [ "$AUTO_START" = 1 ] && [ -n "${TRECD_PID:-}" ]; then
        kill "$TRECD_PID" 2>/dev/null || true
        wait "$TRECD_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# ── Create dialplan ────────────────────────────────────────────────

DIALPLAN="$TMPDIR/dialplan.json"
cat > "$DIALPLAN" <<JSON
{
  "extensions": {
    "echo": { "action": "echo" }
  }
}
JSON

# ── Auto-start trecd ───────────────────────────────────────────────

if [ "$AUTO_START" = 1 ]; then
    echo "--- building trecd ---"
    if ! go build -o "$TMPDIR/trecd" "$ROOT/cmd/trecsd/" 2>&1; then
        fail "trecd build failed"
        exit 1
    fi
    pass "trecd built"

    echo "--- starting trecd on $TARGET ---"
    "$TMPDIR/trecd" -addr "$TARGET" -dialplan "$DIALPLAN" &
    TRECD_PID=$!
    sleep 2
    if ! kill -0 "$TRECD_PID" 2>/dev/null; then
        fail "trecd failed to start"
        exit 1
    fi
    pass "trecd started on $TARGET with dialplan"
fi

# ── Generate test tone ────────────────────────────────────────────

TONE_FILE="$TMPDIR/tone.wav"
RECV_FILE="$TMPDIR/recv.wav"

echo ""
echo "=== sox tone generation ==="
sox -n -b 16 -r 8000 -c 1 "$TONE_FILE" synth "$DURATION" sine 440 2>&1
if [ -f "$TONE_FILE" ] && [ "$(file_size "$TONE_FILE")" -gt 44 ]; then
    pass "tone file ($(file_size "$TONE_FILE") bytes)"
else
    fail "tone file missing or too small"
    exit 1
fi

# ── Run pjsua echo call via dialplan ───────────────────────────────

SIP_PARAMS=""
[ "$PROTO" = "tcp" ] && SIP_PARAMS=";transport=tcp"

echo ""
echo "=== pjsua echo call (dialplan, ${PROTO}) ==="
PJSUA_LOG="$TMPDIR/pjsua.log"

(
    echo "sleep $DURATION"
    sleep $((DURATION + 3))
) | pjsua \
    --rtp-port 13000 \
    --id "sip:caller@127.0.0.1${SIP_PARAMS}" \
    --registrar "sip:${TARGET}${SIP_PARAMS}" \
    --realm "*" \
    --play-file "$TONE_FILE" \
    --null-audio \
    --auto-play \
    --auto-rec \
    --rec-file "$RECV_FILE" \
    --duration "$DURATION" \
    $([ "$PROTO" = "tcp" ] && echo "--no-udp") \
    "sip:echo@${TARGET}${SIP_PARAMS}" \
    > "$PJSUA_LOG" 2>&1 || true

echo "--- pjsua log highlights ---"
grep -E "registration success|state changed to CONFIRMED|DISCONNECTED" "$PJSUA_LOG" \
    | sed 's/^/  /' || echo "  (no matching log lines)"

# ── Verify ─────────────────────────────────────────────────────────

echo ""
echo "=== checking results (${PROTO}) ==="

if grep -q "registration success" "$PJSUA_LOG"; then
    pass "SIP registration succeeded"
else
    fail "SIP registration"
fi

if grep -q "state changed to CONFIRMED" "$PJSUA_LOG"; then
    pass "call reached CONFIRMED state"
else
    fail "call did not reach CONFIRMED state"
fi

if [ -f "$RECV_FILE" ]; then
    WAV_DATA=$(( $(file_size "$RECV_FILE") - 44 ))
    if [ "$WAV_DATA" -gt 0 ]; then
        pass "dialplan echo: received audio ($WAV_DATA bytes)"
    else
        fail "dialplan echo: no audio data"
    fi
else
    fail "dialplan echo: recording not found"
fi

echo ""
echo "=== results: ${PASS} passed, ${FAIL} failed ==="

if [ "$FAIL" -gt 0 ] && [ -f "$PJSUA_LOG" ]; then
    echo "=== pjsua log ==="
    head -100 "$PJSUA_LOG" 2>/dev/null || echo "(empty)"
fi

exit $FAIL
