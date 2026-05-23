#!/usr/bin/env bash
#
# test_dialplan_play.sh — end-to-end dialplan file playback test
#
# Loads trecd with a dialplan that routes "play" → play a tone file,
# calls the extension via pjsua, and verifies the tone is received.
#
# Prerequisites:
#   - trecd built (see scripts/run_trecd.sh) or use -s
#   - sox installed (brew install sox)
#   - pjsua installed (brew install pjsua-ua)
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-s] [-t target] [-p proto]

Dialplan file-playback test: call sip:play@... and receive audio.

Options:
  -s          Auto-start trecd before the test
  -t target   Server address (default: 127.0.0.1:5061)
  -p proto    SIP transport: udp or tcp (default: udp)
  -h          Show this help and exit
EOF
    exit 0
}

AUTO_START=0
TARGET="127.0.0.1:5061"
PROTO="udp"

while getopts ":hst:p:" opt; do
    case "$opt" in
        h) usage ;;
        s) AUTO_START=1 ;;
        t) TARGET="$OPTARG" ;;
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

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMPDIR=$(mktemp -d /tmp/trec_dp_play.XXXXXX) || { echo "FAIL: mktemp"; exit 1; }

cleanup() {
    rm -rf "$TMPDIR"
    if [ "$AUTO_START" = 1 ] && [ -n "${TRECD_PID:-}" ]; then
        kill "$TRECD_PID" 2>/dev/null || true
        wait "$TRECD_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# ── Generate a tone file ~3 seconds ───────────────────────────────

TONE_FILE="$TMPDIR/tone.wav"
echo "=== sox tone generation ==="
sox -n -b 16 -r 8000 -c 1 "$TONE_FILE" synth 3 sine 660 2>&1
if [ -f "$TONE_FILE" ] && [ "$(stat -f%z "$TONE_FILE")" -gt 44 ]; then
    pass "tone file ($(stat -f%z "$TONE_FILE") bytes)"
else
    fail "tone file missing or too small"
    exit 1
fi
TONE_BYTES=$(( $(stat -f%z "$TONE_FILE") - 44 ))

# ── Create dialplan pointing to the tone file ─────────────────────

DIALPLAN="$TMPDIR/dialplan.json"
cat > "$DIALPLAN" <<JSON
{
  "extensions": {
    "play": { "action": "play", "file": "$TONE_FILE" }
  }
}
JSON

# ── Build and start trecd ─────────────────────────────────────────

if [ "$AUTO_START" = 1 ]; then
    echo "--- building trecd ---"
    if ! rtk go build -o "$TMPDIR/trecd" "$ROOT/cmd/trecd/" 2>&1; then
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

# ── Run pjsua call to file playback ───────────────────────────────

SIP_PARAMS=""
[ "$PROTO" = "tcp" ] && SIP_PARAMS=";transport=tcp"

RECV_FILE="$TMPDIR/recv.wav"
PJSUA_LOG="$TMPDIR/pjsua.log"

echo ""
echo "=== pjsua call to file playback (${PROTO}) ==="

(
    echo "sleep 6000"
    sleep 10
) | pjsua \
    --id "sip:listener@127.0.0.1${SIP_PARAMS}" \
    --registrar "sip:${TARGET}${SIP_PARAMS}" \
    --realm "*" \
    --null-audio \
    --auto-rec \
    --rec-file "$RECV_FILE" \
    $([ "$PROTO" = "tcp" ] && echo "--no-udp") \
    "sip:play@${TARGET}${SIP_PARAMS}" \
    > "$PJSUA_LOG" 2>&1 || true

echo "--- pjsua log highlights ---"
grep -E "registration success|state changed to CONFIRMED|state changed to DISCONNECTED|DISCONNECTED" "$PJSUA_LOG" \
    | sed 's/^/  /' || echo "  (no matching log lines)"

# ── Verify results ────────────────────────────────────────────────

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

CALL_DISCONNECTED=0
if grep -q "DISCONNECTED" "$PJSUA_LOG"; then
    pass "call was disconnected (playback ended)"
    CALL_DISCONNECTED=1
else
    fail "call was not disconnected (playback may not have completed)"
fi

if [ -f "$RECV_FILE" ]; then
    RECV_BYTES=$(( $(stat -f%z "$RECV_FILE") - 44 ))
    if [ "$RECV_BYTES" -gt 0 ]; then
        pass "received audio ($RECV_BYTES bytes)"
        DUR=$(sox --info -D "$RECV_FILE" 2>/dev/null || echo 0)
        echo "  received duration: ${DUR}s"
    else
        fail "received no audio"
    fi
else
    fail "recording file not found"
fi

echo ""
echo "=== results: ${PASS} passed, ${FAIL} failed ==="
exit $FAIL
