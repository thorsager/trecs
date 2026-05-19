#!/usr/bin/env bash
#
# test_b2bua_full.sh — B2BUA edge-case integration tests
#
# Runs multiple test scenarios for the trec B2BUA.
#
# Prerequisites:
#   - trecd running on TARGET (see scripts/run_trecd.sh) or use -s
#   - sox installed (brew install sox)
#   - pjsua installed (brew install pjsua-ua)
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-s] [-t target]

Run B2BUA edge-case integration tests.

Options:
  -s          Auto-start trecd before the test
  -t target   Server address (default: 127.0.0.1:5061)
  -h          Show this help and exit
EOF
    exit 0
}

AUTO_START=0
TARGET="127.0.0.1:5061"

while getopts ":hst:" opt; do
    case "$opt" in
        h) usage ;;
        s) AUTO_START=1 ;;
        t) TARGET="$OPTARG" ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage ;;
    esac
done
shift $((OPTIND - 1))

PASS=0
FAIL=0

pass() { echo "  ✓ $1"; ((PASS++)); }
fail() { echo "  ✗ $1"; ((FAIL++)); }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Use a temp dir for all test artifacts
TMPDIR=$(mktemp -d /tmp/trec_b2bua_test.XXXXXX) || { echo "FAIL: mktemp"; exit 1; }

cleanup_all() {
    rm -rf "$TMPDIR"
    if [ "$AUTO_START" = 1 ] && [ -n "${TRECD_PID:-}" ]; then
        kill "$TRECD_PID" 2>/dev/null || true
        wait "$TRECD_PID" 2>/dev/null || true
    fi
}
trap cleanup_all EXIT

# ── Auto-start trecd ──────────────────────────────────────────────

if [ "$AUTO_START" = 1 ]; then
    echo "--- building trecd ---"
    if ! rtk go build -o /tmp/trecd_b2bua_test "$ROOT/cmd/trecd/" 2>&1; then
        fail "trecd build failed"
        exit 1
    fi
    echo "--- starting trecd on $TARGET ---"
    /tmp/trecd_b2bua_test -addr "$TARGET" > "$TMPDIR/trecd.log" 2>&1 &
    TRECD_PID=$!
    sleep 2
    if ! kill -0 "$TRECD_PID" 2>/dev/null; then
        fail "trecd failed to start"
        exit 1
    fi
    pass "trecd started on $TARGET"
fi

# ── Generate test tone ────────────────────────────────────────────

echo ""
echo "=== generating 440Hz test tone ==="
TONE_FILE="$TMPDIR/tone.wav"
sox -n -b 16 -r 8000 -c 1 "$TONE_FILE" synth 4 sine 440 2>&1
if [ -f "$TONE_FILE" ] && [ "$(stat -f%z "$TONE_FILE")" -gt 44 ]; then
    pass "tone file created ($(stat -f%z "$TONE_FILE") bytes)"
else
    fail "tone file missing"
    exit 1
fi

HOST="127.0.0.1"
ALICE_PORT=15062
BOB_PORT=15063

# ==================================================================
# SCENARIO 1: basic — BYE from Alice (Alice hangs up)
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  Scenario 1: basic — BYE from Alice"
echo "═══════════════════════════════════════════════════════════════"

BOB_LOG1="$TMPDIR/bob1.log"
ALICE_LOG1="$TMPDIR/alice1.log"
BOB_RECV1="$TMPDIR/bob1_recv.wav"

# Bob (callee) — keep alive for 10s via pipe
(sleep 10) | pjsua \
    --local-port "$BOB_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 200 \
    --null-audio \
    --rec-file "$BOB_RECV1" \
    --auto-rec \
    > "$BOB_LOG1" 2>&1 &
BOB_PID1=$!
sleep 3

if ! kill -0 "$BOB_PID1" 2>/dev/null; then
    fail "[S1] Bob pjsua failed to start"
else
    pass "[S1] Bob started (PID $BOB_PID1)"

    # Alice (caller) — call Bob, stay alive 6s, then exit (BYE)
    (sleep 6) | pjsua \
        --local-port "$ALICE_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        --null-audio \
        --play-file "$TONE_FILE" \
        --auto-play \
        "sip:bob@${TARGET}" \
        > "$ALICE_LOG1" 2>&1 &
    ALICE_PID1=$!

    wait "$ALICE_PID1" 2>/dev/null || true
    sleep 1
    kill "$BOB_PID1" 2>/dev/null || true
    wait "$BOB_PID1" 2>/dev/null || true

    for log in "$BOB_LOG1" "$ALICE_LOG1"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "state changed to CONFIRMED" "$log"; then
            pass "[S1] $who — call CONFIRMED"
        else
            fail "[S1] $who — call not CONFIRMED"
        fi
    done

    if [ -f "$BOB_RECV1" ]; then
        BOB_DATA=$(( $(stat -f%z "$BOB_RECV1") - 44 ))
        if [ "$BOB_DATA" -gt 0 ]; then
            pass "[S1] Bob received audio ($BOB_DATA bytes)"
        else
            fail "[S1] Bob received no audio"
        fi
    else
        fail "[S1] Bob recording not found"
    fi
fi

sleep 2

# ==================================================================
# SCENARIO 2: bob-bye — Bob hangs up first
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  Scenario 2: bob-bye — Bob hangs up first"
echo "═══════════════════════════════════════════════════════════════"

BOB_LOG2="$TMPDIR/bob2.log"
ALICE_LOG2="$TMPDIR/alice2.log"

# Bob (callee) — auto-hangup after 3s, keep alive 10s
(sleep 10) | pjsua \
    --local-port "$BOB_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 200 \
    --duration 3 \
    --null-audio \
    > "$BOB_LOG2" 2>&1 &
BOB_PID2=$!
sleep 3

if ! kill -0 "$BOB_PID2" 2>/dev/null; then
    fail "[S2] Bob pjsua failed to start"
else
    pass "[S2] Bob started (PID $BOB_PID2)"

    # Alice (caller) — stay alive 8s, Bob will hang up first
    (sleep 8) | pjsua \
        --local-port "$ALICE_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        --null-audio \
        "sip:bob@${TARGET}" \
        > "$ALICE_LOG2" 2>&1 &
    ALICE_PID2=$!

    wait "$ALICE_PID2" 2>/dev/null || true
    sleep 1
    kill "$BOB_PID2" 2>/dev/null || true
    wait "$BOB_PID2" 2>/dev/null || true

    for log in "$BOB_LOG2" "$ALICE_LOG2"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "state changed to CONFIRMED" "$log"; then
            pass "[S2] $who — call CONFIRMED"
        else
            fail "[S2] $who — call not CONFIRMED"
        fi
    done

    if grep -qi "Disconnected" "$ALICE_LOG2" 2>/dev/null; then
        pass "[S2] Alice received BYE from Bob (disconnected)"
    else
        fail "[S2] Alice did not see disconnection"
    fi
fi

sleep 2

# ==================================================================
# SCENARIO 3: reject — Bob responds 486 Busy Here
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  Scenario 3: reject — Bob responds 486 Busy Here"
echo "═══════════════════════════════════════════════════════════════"

BOB_LOG3="$TMPDIR/bob3.log"
ALICE_LOG3="$TMPDIR/alice3.log"

# Bob (callee) — auto-answer with 486, keep alive 10s
(sleep 10) | pjsua \
    --local-port "$BOB_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 486 \
    --null-audio \
    > "$BOB_LOG3" 2>&1 &
BOB_PID3=$!
sleep 3

if ! kill -0 "$BOB_PID3" 2>/dev/null; then
    fail "[S3] Bob pjsua failed to start"
else
    pass "[S3] Bob started (PID $BOB_PID3)"

    # Alice (caller) — call will be rejected
    (sleep 2) | pjsua \
        --local-port "$ALICE_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        --null-audio \
        "sip:bob@${TARGET}" \
        > "$ALICE_LOG3" 2>&1 &
    ALICE_PID3=$!

    wait "$ALICE_PID3" 2>/dev/null || true
    sleep 1
    kill "$BOB_PID3" 2>/dev/null || true
    wait "$BOB_PID3" 2>/dev/null || true

    if grep -qi "486" "$ALICE_LOG3" 2>/dev/null; then
        pass "[S3] Alice received 486 Busy Here (call rejected)"
    else
        fail "[S3] Alice did not receive 486"
    fi
fi

# ==================================================================
# SCENARIO 4: both TCP — basic call, BYE from Alice
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  Scenario 4: both TCP — basic call, BYE from Alice"
echo "═══════════════════════════════════════════════════════════════"

BOB_LOG4="$TMPDIR/bob4.log"
ALICE_LOG4="$TMPDIR/alice4.log"
BOB_RECV4="$TMPDIR/bob4_recv.wav"

(sleep 10) | pjsua \
    --local-port "$BOB_PORT" \
    --id "sip:bob@${HOST};transport=tcp" \
    --registrar "sip:${TARGET};transport=tcp" \
    --realm "*" \
    --no-udp \
    --auto-answer 200 \
    --null-audio \
    --rec-file "$BOB_RECV4" \
    --auto-rec \
    > "$BOB_LOG4" 2>&1 &
BOB_PID4=$!
sleep 3

if ! kill -0 "$BOB_PID4" 2>/dev/null; then
    fail "[S4] Bob pjsua failed to start"
else
    pass "[S4] Bob started (PID $BOB_PID4, TCP)"

    (sleep 6) | pjsua \
        --local-port "$ALICE_PORT" \
        --id "sip:alice@${HOST};transport=tcp" \
        --registrar "sip:${TARGET};transport=tcp" \
        --realm "*" \
        --no-udp \
        --null-audio \
        --play-file "$TONE_FILE" \
        --auto-play \
        "sip:bob@${TARGET};transport=tcp" \
        > "$ALICE_LOG4" 2>&1 &
    ALICE_PID4=$!

    wait "$ALICE_PID4" 2>/dev/null || true
    sleep 1
    kill "$BOB_PID4" 2>/dev/null || true
    wait "$BOB_PID4" 2>/dev/null || true

    for log in "$BOB_LOG4" "$ALICE_LOG4"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "state changed to CONFIRMED" "$log"; then
            pass "[S4] $who — call CONFIRMED (TCP)"
        else
            fail "[S4] $who — call not CONFIRMED"
        fi
    done

    if [ -f "$BOB_RECV4" ]; then
        BOB_DATA=$(( $(stat -f%z "$BOB_RECV4") - 44 ))
        if [ "$BOB_DATA" -gt 0 ]; then
            pass "[S4] Bob received audio ($BOB_DATA bytes, TCP)"
        else
            fail "[S4] Bob received no audio"
        fi
    else
        fail "[S4] Bob recording not found"
    fi
fi

sleep 2

# ==================================================================
# SCENARIO 5: Alice TCP, Bob UDP — basic call, BYE from Alice
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  Scenario 5: Alice TCP, Bob UDP — basic call, BYE from Alice"
echo "═══════════════════════════════════════════════════════════════"

BOB_LOG5="$TMPDIR/bob5.log"
ALICE_LOG5="$TMPDIR/alice5.log"
BOB_RECV5="$TMPDIR/bob5_recv.wav"

# Bob (UDP)
(sleep 10) | pjsua \
    --local-port "$BOB_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 200 \
    --null-audio \
    --rec-file "$BOB_RECV5" \
    --auto-rec \
    > "$BOB_LOG5" 2>&1 &
BOB_PID5=$!
sleep 3

if ! kill -0 "$BOB_PID5" 2>/dev/null; then
    fail "[S5] Bob pjsua failed to start"
else
    pass "[S5] Bob started (PID $BOB_PID5, UDP)"

    # Alice (TCP)
    (sleep 6) | pjsua \
        --local-port "$ALICE_PORT" \
        --id "sip:alice@${HOST};transport=tcp" \
        --registrar "sip:${TARGET};transport=tcp" \
        --realm "*" \
        --no-udp \
        --null-audio \
        --play-file "$TONE_FILE" \
        --auto-play \
        "sip:bob@${TARGET};transport=tcp" \
        > "$ALICE_LOG5" 2>&1 &
    ALICE_PID5=$!

    wait "$ALICE_PID5" 2>/dev/null || true
    sleep 1
    kill "$BOB_PID5" 2>/dev/null || true
    wait "$BOB_PID5" 2>/dev/null || true

    for log in "$BOB_LOG5" "$ALICE_LOG5"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "state changed to CONFIRMED" "$log"; then
            pass "[S5] $who — call CONFIRMED (Alice TCP, Bob UDP)"
        else
            fail "[S5] $who — call not CONFIRMED"
        fi
    done

    if [ -f "$BOB_RECV5" ]; then
        BOB_DATA=$(( $(stat -f%z "$BOB_RECV5") - 44 ))
        if [ "$BOB_DATA" -gt 0 ]; then
            pass "[S5] Bob received audio ($BOB_DATA bytes, Alice TCP)"
        else
            fail "[S5] Bob received no audio"
        fi
    else
        fail "[S5] Bob recording not found"
    fi
fi

sleep 2

# ==================================================================
# SCENARIO 6: Alice UDP, Bob TCP — basic call, BYE from Alice
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  Scenario 6: Alice UDP, Bob TCP — basic call, BYE from Alice"
echo "═══════════════════════════════════════════════════════════════"

BOB_LOG6="$TMPDIR/bob6.log"
ALICE_LOG6="$TMPDIR/alice6.log"
BOB_RECV6="$TMPDIR/bob6_recv.wav"

# Bob (TCP)
(sleep 10) | pjsua \
    --local-port "$BOB_PORT" \
    --id "sip:bob@${HOST};transport=tcp" \
    --registrar "sip:${TARGET};transport=tcp" \
    --realm "*" \
    --no-udp \
    --auto-answer 200 \
    --null-audio \
    --rec-file "$BOB_RECV6" \
    --auto-rec \
    > "$BOB_LOG6" 2>&1 &
BOB_PID6=$!
sleep 3

if ! kill -0 "$BOB_PID6" 2>/dev/null; then
    fail "[S6] Bob pjsua failed to start"
else
    pass "[S6] Bob started (PID $BOB_PID6, TCP)"

    # Alice (UDP)
    (sleep 6) | pjsua \
        --local-port "$ALICE_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        --null-audio \
        --play-file "$TONE_FILE" \
        --auto-play \
        "sip:bob@${TARGET}" \
        > "$ALICE_LOG6" 2>&1 &
    ALICE_PID6=$!

    wait "$ALICE_PID6" 2>/dev/null || true
    sleep 1
    kill "$BOB_PID6" 2>/dev/null || true
    wait "$BOB_PID6" 2>/dev/null || true

    for log in "$BOB_LOG6" "$ALICE_LOG6"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "state changed to CONFIRMED" "$log"; then
            pass "[S6] $who — call CONFIRMED (Alice UDP, Bob TCP)"
        else
            fail "[S6] $who — call not CONFIRMED"
        fi
    done

    if [ -f "$BOB_RECV6" ]; then
        BOB_DATA=$(( $(stat -f%z "$BOB_RECV6") - 44 ))
        if [ "$BOB_DATA" -gt 0 ]; then
            pass "[S6] Bob received audio ($BOB_DATA bytes, Bob TCP)"
        else
            fail "[S6] Bob received no audio"
        fi
    else
        fail "[S6] Bob recording not found"
    fi
fi

# ==================================================================
# Summary
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  results: ${PASS} passed, ${FAIL} failed (6 scenarios)"
echo "═══════════════════════════════════════════════════════════════"
exit $FAIL
