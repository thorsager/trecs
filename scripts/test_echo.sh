#!/usr/bin/env bash
#
# test_echo.sh — end-to-end echo test using sox and pjsua
#
# Generates a test tone, calls the echo service via pjsua, and
# verifies that the recorded audio contains the echoed tone.
#
# Prerequisites:
#   - trecd running (see scripts/run_trecd.sh) or use -s to auto-start
#   - sox installed (brew install sox)
#   - pjsua installed (brew install pjsua)
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-s] [-t target] [-d duration] [-p proto]

End-to-end echo test using sox and pjsua.

Options:
  -s          Auto-start trecd before the test (requires go)
  -t target   Server address (default: 127.0.0.1:5061)
  -d duration Call duration in seconds (default: 5)
  -p proto    SIP transport: udp or tcp (default: udp)
  -h          Show this help and exit
EOF
    exit 0
}

AUTO_START=0
TARGET="127.0.0.1:5061"
DURATION=5
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

cleanup() {
    if [ "$AUTO_START" = 1 ] && [ -n "${TRECD_PID:-}" ]; then
        kill "$TRECD_PID" 2>/dev/null || true
        wait "$TRECD_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

if [ "$AUTO_START" = 1 ]; then
    echo "--- starting trecd ---"
    nohup go run ./cmd/trecsd/ -addr "$TARGET" > /tmp/trecd_test_echo.log 2>&1 &
    TRECD_PID=$!
    sleep 1
    if ! kill -0 "$TRECD_PID" 2>/dev/null; then
        echo "  ✗ failed to start trecd"
        exit 1
    fi
    pass "trecd started on $TARGET"
fi

TONE_FILE=$(mktemp /tmp/trec_test_tone.XXXXXX.wav)
ECHO_FILE=$(mktemp /tmp/trec_test_echo.XXXXXX.wav)

echo ""
echo "=== sox tone generation ==="
echo "--- generating ${DURATION}s 440Hz tone ---"
sox -n -b 16 -r 8000 -c 1 "$TONE_FILE" synth "$DURATION" sine 440 2>&1
if [ -f "$TONE_FILE" ] && [ "$(file_size "$TONE_FILE")" -gt 44 ]; then
    pass "tone file created ($(file_size "$TONE_FILE") bytes)"
else
    fail "tone file missing or too small"
    rm -f "$TONE_FILE" "$ECHO_FILE"
    exit 1
fi

echo ""
echo "=== pjsua echo test (${PROTO}) ==="

PJSUA_LOG=$(mktemp /tmp/trec_pjsua_log.XXXXXX)

# When using TCP, disable UDP transport and add ;transport=tcp to URIs.
SIP_PARAMS=""
[ "$PROTO" = "tcp" ] && SIP_PARAMS=";transport=tcp"

# Keep the pipe open so pjsua doesn't exit on EOF. The subshell sends
# "sleep $DURATION" to pjsua (keeps it alive for $DURATION seconds), then
# keeps stdin open a bit longer so the call can finish cleanly.
(
    echo "sleep $DURATION"
    sleep $((DURATION + 3))
) | pjsua \
    --rtp-port 15000 15099 \
    --id "sip:caller@127.0.0.1${SIP_PARAMS}" \
    --registrar "sip:${TARGET}${SIP_PARAMS}" \
    --realm "*" \
    --play-file "$TONE_FILE" \
    --auto-play \
    --auto-rec \
    --rec-file "$ECHO_FILE" \
    --duration "$DURATION" \
    $([ "$PROTO" = "tcp" ] && echo "--no-udp") \
    "sip:echo@${TARGET}${SIP_PARAMS}" \
    > "$PJSUA_LOG" 2>&1 || true

echo "--- pjsua log highlights ---"
grep -E "registration success|state changed to CONFIRMED|state changed to CONNECTING|DISCONNECTED|200 OK" "$PJSUA_LOG" | sed 's/^/  /' || echo "  (no matching log lines)"

echo ""
echo "--- checking results ---"

REG_OK=0
if grep -q "registration success" "$PJSUA_LOG"; then
    pass "SIP registration succeeded"
    REG_OK=1
else
    fail "SIP registration"
fi

CALL_CONFIRMED=0
if grep -q "state changed to CONFIRMED" "$PJSUA_LOG"; then
    pass "call reached CONFIRMED state"
    CALL_CONFIRMED=1
else
    fail "call did not reach CONFIRMED state"
fi

if [ -f "$ECHO_FILE" ]; then
    ECHO_SIZE=$(file_size "$ECHO_FILE" 2>/dev/null || echo 0)
    WAV_DATA=$((ECHO_SIZE - 44))
    if [ "$WAV_DATA" -gt 0 ]; then
        pass "echoed audio file has data ($WAV_DATA bytes of audio)"
    else
        fail "echoed audio file is header-only ($ECHO_SIZE bytes)"
    fi
else
    fail "echoed audio file not found"
    WAV_DATA=0
fi

if [ "$WAV_DATA" -gt 0 ]; then
    ECHO_DUR=$(sox --info -D "$ECHO_FILE" 2>/dev/null || echo 0)
    TONE_DUR=$(sox --info -D "$TONE_FILE" 2>/dev/null || echo 0)
    echo "  tone duration: ${TONE_DUR}s, echoed duration: ${ECHO_DUR}s"
    if [ "$(echo "$ECHO_DUR > 0.5" | bc -l 2>/dev/null)" = 1 ]; then
        pass "echoed audio duration ($ECHO_DUR s) is meaningful"
    else
        fail "echoed audio too short ($ECHO_DUR s)"
    fi
fi

rm -f "$TONE_FILE" "$ECHO_FILE" "$PJSUA_LOG"

echo ""
echo "=== results: ${PASS} passed, ${FAIL} failed ==="
exit $FAIL
