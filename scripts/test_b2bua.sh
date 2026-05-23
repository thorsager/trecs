#!/usr/bin/env bash
#
# test_b2bua.sh — B2BUA call: Alice ↔ trec ↔ Bob via pjsua
#
# Starts two pjsua instances (Alice + Bob), both register with trec.
# Alice places a call to sip:bob@... which trec forwards to Bob as a
# B2BUA. Bob auto-answers, media flows through trec's RTP bridge.
#
# Prerequisites:
#   - trecd running on TARGET (see scripts/run_trecd.sh) or use -s
#   - sox installed (brew install sox)
#   - pjsua-ua installed (brew install pjsua-ua)
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-s] [-t target] [-d duration]

B2BUA call test: Alice ↔ trec ↔ Bob via pjsua.

Options:
  -s          Auto-start trecd before the test
  -t target   Server address (default: 127.0.0.1:5061)
  -d duration Call duration in seconds (default: 5)
  -h          Show this help and exit
EOF
    exit 0
}

AUTO_START=0
TARGET="127.0.0.1:5061"
DURATION=5

while getopts ":hst:d:" opt; do
    case "$opt" in
        h) usage ;;
        s) AUTO_START=1 ;;
        t) TARGET="$OPTARG" ;;
        d) DURATION="$OPTARG" ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage ;;
    esac
done
shift $((OPTIND - 1))

PASS=0
FAIL=0

pass() { echo "  ✓ $1"; ((PASS++)); }
fail() { echo "  ✗ $1"; ((FAIL++)); }

file_size() { stat -c %s "$1" 2>/dev/null || stat -f%z "$1" 2>/dev/null; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cleanup() {
    echo ""
    echo "--- cleaning up ---"
    for pid in "${BOB_PID:-}" "${ALICE_PID:-}"; do
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [ "$AUTO_START" = 1 ] && [ -n "${TRECD_PID:-}" ]; then
        kill "$TRECD_PID" 2>/dev/null || true
        wait "$TRECD_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# ── Build trecd binary ────────────────────────────────────────────

if [ "$AUTO_START" = 1 ]; then
    echo "--- building trecd ---"
    if ! go build -o /tmp/trecd_b2bua_test "$ROOT/cmd/trecsd/" 2>&1; then
        fail "trecd build failed"
        exit 1
    fi
    pass "trecd built"
fi

# ── Auto-start trecd ──────────────────────────────────────────────

if [ "$AUTO_START" = 1 ]; then
    echo "--- starting trecd on $TARGET ---"
    /tmp/trecd_b2bua_test -addr "$TARGET" &
    TRECD_PID=$!
    sleep 2
    if ! kill -0 "$TRECD_PID" 2>/dev/null; then
        fail "trecd failed to start"
        exit 1
    fi
    pass "trecd started on $TARGET"
fi

# ── Generate test tone ────────────────────────────────────────────

TONE_FILE=/tmp/trec_b2bua_tone.wav
echo ""
echo "=== sox tone generation ==="
echo "--- generating ${DURATION}s 440Hz tone ---"
sox -n -b 16 -r 8000 -c 1 "$TONE_FILE" synth "$DURATION" sine 440 2>&1
if [ -f "$TONE_FILE" ] && [ "$(file_size "$TONE_FILE")" -gt 44 ]; then
    pass "tone file ($(file_size "$TONE_FILE") bytes)"
else
    fail "tone file missing or too small"
    exit 1
fi

# ── Pick ports for the two pjsua instances ────────────────────────
# Avoid 15060/15061 (used by other processes if present).
ALICE_PORT=15062
BOB_PORT=15063
HOST="127.0.0.1"

# ── Start Bob (callee) ────────────────────────────────────────────

echo ""
echo "=== starting Bob (callee) on port ${BOB_PORT} ==="
BOB_LOG=$(mktemp /tmp/trec_b2bua_bob.XXXXXX.log)
BOB_RECV=$(mktemp /tmp/trec_b2bua_bob_recv.XXXXXX.wav)

(
    # "sleep" keeps pjsua alive while waiting for calls
    echo "sleep $((DURATION * 3000))"
    sleep $((DURATION + 15))
) | pjsua \
    --config-file /dev/null \
    --local-port "$BOB_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 200 \
    --null-audio \
    --rec-file "$BOB_RECV" \
    --auto-rec \
    > "$BOB_LOG" 2>&1 &
BOB_PID=$!
sleep 4

if ! kill -0 "$BOB_PID" 2>/dev/null; then
    fail "Bob pjsua failed to start"
    exit 1
fi
pass "Bob pjsua started (PID $BOB_PID, port $BOB_PORT)"

# ── Start Alice (caller) ──────────────────────────────────────────

echo ""
echo "=== starting Alice (caller) on port ${ALICE_PORT} ==="
ALICE_LOG=$(mktemp /tmp/trec_b2bua_alice.XXXXXX.log)
ALICE_RECV=$(mktemp /tmp/trec_b2bua_alice_recv.XXXXXX.wav)

(
    echo "sleep $((DURATION * 1000))"
    sleep $((DURATION + 10))
) | pjsua \
    --config-file /dev/null \
    --local-port "$ALICE_PORT" \
    --id "sip:alice@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --null-audio \
    --play-file "$TONE_FILE" \
    --auto-play \
    --rec-file "$ALICE_RECV" \
    --auto-rec \
    "sip:bob@${TARGET}" \
    > "$ALICE_LOG" 2>&1 &
ALICE_PID=$!

# Wait for Alice to finish (exits after the "sleep" command)
wait "$ALICE_PID" 2>/dev/null || true
ALICE_PID=""

echo ""
echo "=== log highlights ==="
echo "--- Bob ---"
grep -iE "CONFIRMED|registration success|200/INVITE" "$BOB_LOG" \
    | sed 's/^/  /' || echo "  (no matching lines)"
echo "--- Alice ---"
grep -iE "CONFIRMED|registration success|200/INVITE" "$ALICE_LOG" \
    | sed 's/^/  /' || echo "  (no matching lines)"

# ── Verify results ────────────────────────────────────────────────

echo ""
echo "=== checking results ==="

# Registration
for log in "$BOB_LOG" "$ALICE_LOG"; do
    if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
    if grep -qi "registration success" "$log"; then
        pass "$who registered"
    else
        fail "$who did not register"
    fi
done

# Call CONFIRMED on both sides
for log in "$BOB_LOG" "$ALICE_LOG"; do
    if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
    if grep -qi "state changed to CONFIRMED" "$log"; then
        pass "$who — call CONFIRMED"
    else
        fail "$who — call not CONFIRMED"
    fi
done

# Bob's recording has audio data (Alice's tone forwarded through bridge)
if [ -f "$BOB_RECV" ]; then
    BOB_WAV_DATA=$(( $(file_size "$BOB_RECV") - 44 ))
    if [ "$BOB_WAV_DATA" -gt 0 ]; then
        pass "Bob received audio ($BOB_WAV_DATA bytes)"
    else
        fail "Bob received no audio"
    fi
else
    fail "Bob recording file not found"
fi

# Alice's recording (may be silent since Bob uses --null-audio)
if [ -f "$ALICE_RECV" ]; then
    ALICE_WAV_DATA=$(( $(file_size "$ALICE_RECV") - 44 ))
    if [ "$ALICE_WAV_DATA" -gt 0 ]; then
        pass "Alice received audio ($ALICE_WAV_DATA bytes)"
    fi
fi

echo ""
echo "=== results: ${PASS} passed, ${FAIL} failed ==="
exit $FAIL
