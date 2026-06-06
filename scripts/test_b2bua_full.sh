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

source "$(cd "$(dirname "$0")/.." && pwd)/scripts/lib/pjsua.sh"

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

file_size() { stat -c %s "$1" 2>/dev/null || stat -f%z "$1" 2>/dev/null; }

check_audio() {
    local label="$1" file="$2" direction="$3"
    if [ -f "$file" ]; then
        local data=$(( $(file_size "$file") - 44 ))
        if [ "$data" -gt 0 ]; then
            pass "[${label}] ${direction} received audio ($data bytes)"
        else
            fail "[${label}] ${direction} received no audio"
        fi
    else
        fail "[${label}] ${direction} recording not found"
    fi
}

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

LOCAL_PORT=12000
RTP_PORT=4000

next_ports() {
    ((LOCAL_PORT++))
    ((RTP_PORT++))
}

# ── Auto-start trecd ──────────────────────────────────────────────

if [ "$AUTO_START" = 1 ]; then
    echo "--- building trecd ---"
    if ! go build -o /tmp/trecd_b2bua_test "$ROOT/cmd/trecsd/" 2>&1; then
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



# ── Generate test tones ───────────────────────────────────────────

echo ""
echo "=== generating test tones ==="
TONE_FILE="$TMPDIR/tone.wav"     # 440Hz — played by Alice
TONE_FILE2="$TMPDIR/tone2.wav"   # 880Hz — played by Bob
sox -n -b 16 -r 8000 -c 1 "$TONE_FILE"  synth 4 sine 440 2>&1
sox -n -b 16 -r 8000 -c 1 "$TONE_FILE2" synth 4 sine 880 2>&1
if [ -f "$TONE_FILE" ] && [ "$(file_size "$TONE_FILE")" -gt 44 ]; then
    pass "tone file (440Hz) created ($(file_size "$TONE_FILE") bytes)"
else
    fail "tone file (440Hz) missing"
    exit 1
fi
if [ -f "$TONE_FILE2" ] && [ "$(file_size "$TONE_FILE2")" -gt 44 ]; then
    pass "tone file (880Hz) created ($(file_size "$TONE_FILE2") bytes)"
else
    fail "tone file (880Hz) missing"
    exit 1
fi

HOST="127.0.0.1"

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
ALICE_RECV1="$TMPDIR/alice1_recv.wav"

# Bob (callee) — keep alive for 10s
next_ports
run_pjsua 10 "$BOB_LOG1" \
    --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
    --local-port "$LOCAL_PORT" \
    --rtp-port "$RTP_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 200 \
    --rec-file "$BOB_RECV1" \
    --auto-rec \
    --play-file "$TONE_FILE2" \
    --auto-play \
    &
BOB_BG_PID1=$!
sleep 5

BOB_PID1=$(cat "${BOB_LOG1}.pid" 2>/dev/null || echo "")
if [ -z "$BOB_PID1" ] || ! kill -0 "$BOB_PID1" 2>/dev/null; then
    fail "[S1] Bob pjsua failed to start"
else
    pass "[S1] Bob started (PID $BOB_PID1)"

    # Alice (caller) — call Bob, stay alive 6s, then exit (BYE)
    next_ports
    run_pjsua 8 "$ALICE_LOG1" \
        --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
        --local-port "$LOCAL_PORT" \
        --rtp-port "$RTP_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        --play-file "$TONE_FILE" \
        --auto-play \
        --rec-file "$ALICE_RECV1" \
        --auto-rec \
        "sip:bob@${TARGET}"

    sleep 1
    kill "$BOB_PID1" 2>/dev/null || true
    wait "$BOB_BG_PID1" 2>/dev/null || true

    for log in "$BOB_LOG1" "$ALICE_LOG1"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "CONFIRMED" "$log"; then
            pass "[S1] $who — call CONFIRMED"
        else
            fail "[S1] $who — call not CONFIRMED"
            echo "--- $log ---"
            cat "$log" 2>/dev/null || echo "(empty)"
            echo ""
            exit 
        fi
    done

    check_audio "S1" "$BOB_RECV1"   "Bob"
    check_audio "S1" "$ALICE_RECV1" "Alice"
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
next_ports
run_pjsua 10 "$BOB_LOG2" \
    --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
    --local-port "$LOCAL_PORT" \
    --rtp-port "$RTP_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 200 \
    --duration 3 \
    &
BOB_BG_PID2=$!
sleep 3

BOB_PID2=$(cat "${BOB_LOG2}.pid" 2>/dev/null || echo "")
if [ -z "$BOB_PID2" ] || ! kill -0 "$BOB_PID2" 2>/dev/null; then
    fail "[S2] Bob pjsua failed to start"
else
    pass "[S2] Bob started (PID $BOB_PID2)"

    # Alice (caller) — stay alive 8s, Bob will hang up first
    next_ports
    run_pjsua 8 "$ALICE_LOG2" \
        --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
        --local-port "$LOCAL_PORT" \
        --rtp-port "$RTP_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        "sip:bob@${TARGET}"

    sleep 1
    kill "$BOB_PID2" 2>/dev/null || true
    wait "$BOB_BG_PID2" 2>/dev/null || true

    for log in "$BOB_LOG2" "$ALICE_LOG2"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "CONFIRMED" "$log"; then
            pass "[S2] $who — call CONFIRMED"
        else
            fail "[S2] $who — call not CONFIRMED"
            echo "--- $log (last 50 lines) ---"
            tail -50 "$log" 2>/dev/null || echo "(empty)"
            echo ""
            exit 
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
next_ports
run_pjsua 10 "$BOB_LOG3" \
    --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
    --local-port "$LOCAL_PORT" \
    --rtp-port "$RTP_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 486 \
    &
BOB_BG_PID3=$!
sleep 3

BOB_PID3=$(cat "${BOB_LOG3}.pid" 2>/dev/null || echo "")
if [ -z "$BOB_PID3" ] || ! kill -0 "$BOB_PID3" 2>/dev/null; then
    fail "[S3] Bob pjsua failed to start"
else
    pass "[S3] Bob started (PID $BOB_PID3)"

    # Alice (caller) — call will be rejected
    next_ports
    run_pjsua 2 "$ALICE_LOG3" \
        --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
        --local-port "$LOCAL_PORT" \
        --rtp-port "$RTP_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        "sip:bob@${TARGET}"

    sleep 1
    kill "$BOB_PID3" 2>/dev/null || true
    wait "$BOB_BG_PID3" 2>/dev/null || true

    if grep -qi "486" "$ALICE_LOG3" 2>/dev/null; then
        pass "[S3] Alice received 486 Busy Here (call rejected)"
    else
        fail "[S3] Alice did not receive 486"
    fi
fi

sleep 2

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
ALICE_RECV4="$TMPDIR/alice4_recv.wav"

next_ports
run_pjsua 10 "$BOB_LOG4" \
    --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
    --local-port "$LOCAL_PORT" \
    --rtp-port "$RTP_PORT" \
    --id "sip:bob@${HOST};transport=tcp" \
    --registrar "sip:${TARGET};transport=tcp" \
    --realm "*" \
    --no-udp \
    --auto-answer 200 \
    --rec-file "$BOB_RECV4" \
    --auto-rec \
    --play-file "$TONE_FILE2" \
    &
BOB_BG_PID4=$!
sleep 3

BOB_PID4=$(cat "${BOB_LOG4}.pid" 2>/dev/null || echo "")
if [ -z "$BOB_PID4" ] || ! kill -0 "$BOB_PID4" 2>/dev/null; then
    fail "[S4] Bob pjsua failed to start"
else
    pass "[S4] Bob started (PID $BOB_PID4, TCP)"

    next_ports
    run_pjsua 6 "$ALICE_LOG4" \
        --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
        --local-port "$LOCAL_PORT" \
        --rtp-port "$RTP_PORT" \
        --id "sip:alice@${HOST};transport=tcp" \
        --registrar "sip:${TARGET};transport=tcp" \
        --realm "*" \
        --no-udp \
        --play-file "$TONE_FILE" \
        --auto-play \
        --rec-file "$ALICE_RECV4" \
        --auto-rec \
        "sip:bob@${TARGET};transport=tcp"

    sleep 1
    kill "$BOB_PID4" 2>/dev/null || true
    wait "$BOB_BG_PID4" 2>/dev/null || true

    for log in "$BOB_LOG4" "$ALICE_LOG4"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "CONFIRMED" "$log"; then
            pass "[S4] $who — call CONFIRMED (TCP)"
        else
            fail "[S4] $who — call not CONFIRMED"
            echo "--- $log (last 50 lines) ---"
            tail -50 "$log" 2>/dev/null || echo "(empty)"
            echo ""
            exit 
        fi
    done

    check_audio "S4" "$BOB_RECV4"   "Bob"
    check_audio "S4" "$ALICE_RECV4" "Alice"
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
ALICE_RECV5="$TMPDIR/alice5_recv.wav"

# Bob (UDP)
next_ports
run_pjsua 10 "$BOB_LOG5" \
    --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
    --local-port "$LOCAL_PORT" \
    --rtp-port "$RTP_PORT" \
    --id "sip:bob@${HOST}" \
    --registrar "sip:${TARGET}" \
    --realm "*" \
    --auto-answer 200 \
    --rec-file "$BOB_RECV5" \
    --auto-rec \
    --play-file "$TONE_FILE2" \
    --auto-play \
    &
BOB_BG_PID5=$!
sleep 3

BOB_PID5=$(cat "${BOB_LOG5}.pid" 2>/dev/null || echo "")
if [ -z "$BOB_PID5" ] || ! kill -0 "$BOB_PID5" 2>/dev/null; then
    fail "[S5] Bob pjsua failed to start"
else
    pass "[S5] Bob started (PID $BOB_PID5, UDP)"

    # Alice (TCP)
    next_ports
    run_pjsua 6 "$ALICE_LOG5" \
        --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
        --local-port "$LOCAL_PORT" \
        --rtp-port "$RTP_PORT" \
        --id "sip:alice@${HOST};transport=tcp" \
        --registrar "sip:${TARGET};transport=tcp" \
        --realm "*" \
        --no-udp \
        --play-file "$TONE_FILE" \
        --auto-play \
        --rec-file "$ALICE_RECV5" \
        --auto-rec \
        "sip:bob@${TARGET};transport=tcp"

    sleep 1
    kill "$BOB_PID5" 2>/dev/null || true
    wait "$BOB_BG_PID5" 2>/dev/null || true

    for log in "$BOB_LOG5" "$ALICE_LOG5"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "CONFIRMED" "$log"; then
            pass "[S5] $who — call CONFIRMED (Alice TCP, Bob UDP)"
        else
            fail "[S5] $who — call not CONFIRMED"
            echo "--- $log (last 50 lines) ---"
            tail -50 "$log" 2>/dev/null || echo "(empty)"
            echo ""
            exit 
        fi
    done

    check_audio "S5" "$BOB_RECV5"   "Bob"
    check_audio "S5" "$ALICE_RECV5" "Alice"
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
ALICE_RECV6="$TMPDIR/alice6_recv.wav"

# Bob (TCP)
next_ports
run_pjsua 10 "$BOB_LOG6" \
    --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
    --local-port "$LOCAL_PORT" \
    --rtp-port "$RTP_PORT" \
    --id "sip:bob@${HOST};transport=tcp" \
    --registrar "sip:${TARGET};transport=tcp" \
    --realm "*" \
    --no-udp \
    --auto-answer 200 \
    --rec-file "$BOB_RECV6" \
    --auto-rec \
    --play-file "$TONE_FILE2" \
    --auto-play \
    &
BOB_BG_PID6=$!
sleep 3

BOB_PID6=$(cat "${BOB_LOG6}.pid" 2>/dev/null || echo "")
if [ -z "$BOB_PID6" ] || ! kill -0 "$BOB_PID6" 2>/dev/null; then
    fail "[S6] Bob pjsua failed to start"
else
    pass "[S6] Bob started (PID $BOB_PID6, TCP)"

    # Alice (UDP)
    next_ports
    run_pjsua 6 "$ALICE_LOG6" \
        --ip-addr 127.0.0.1 --bound-addr 127.0.0.1 \
        --local-port "$LOCAL_PORT" \
        --rtp-port "$RTP_PORT" \
        --id "sip:alice@${HOST}" \
        --registrar "sip:${TARGET}" \
        --realm "*" \
        --play-file "$TONE_FILE" \
        --auto-play \
        --rec-file "$ALICE_RECV6" \
        --auto-rec \
        "sip:bob@${TARGET}"

    sleep 1
    kill "$BOB_PID6" 2>/dev/null || true
    wait "$BOB_BG_PID6" 2>/dev/null || true

    for log in "$BOB_LOG6" "$ALICE_LOG6"; do
        if echo "$log" | grep -q "bob"; then who="Bob"; else who="Alice"; fi
        if grep -qi "CONFIRMED" "$log"; then
            pass "[S6] $who — call CONFIRMED (Alice UDP, Bob TCP)"
        else
            fail "[S6] $who — call not CONFIRMED"
            echo "--- $log (last 50 lines) ---"
            tail -50 "$log" 2>/dev/null || echo "(empty)"
            echo ""
            exit 
        fi
    done

    check_audio "S6" "$BOB_RECV6"   "Bob"
    check_audio "S6" "$ALICE_RECV6" "Alice"
fi

# ==================================================================
# Summary
# ==================================================================

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  results: ${PASS} passed, ${FAIL} failed (6 scenarios)"
echo "═══════════════════════════════════════════════════════════════"


if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "=== pjsua log dump ==="
    for f in "$TMPDIR"/*.log; do
        if [ -f "$f" ]; then
            echo "--- $(basename "$f") ---"
            head -100 "$f" 2>/dev/null || echo "(empty)"
            echo ""
        fi
    done
fi
exit $FAIL
