#!/usr/bin/env bash
#
# test_register.sh — validate SIP registration lifecycle
#
# Tests: register, unregister, concurrent bindings.
#
# Prerequisites:
#   - trecd running (see scripts/run_trecd.sh)
#   - sipsak installed (brew install sipsak)
#
set -uo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-t target] [-p transport]

Validate SIP registration, unregistration, and concurrent bindings.

Options:
  -t target     Server address (default: 127.0.0.1:5061)
  -p transport  Transport protocol: udp (default) or tcp
  -h            Show this help and exit
EOF
    exit 0
}

TARGET="127.0.0.1:5061"
TRANSPORT="udp"
while getopts ":ht:p:" opt; do
    case "$opt" in
        h) usage ;;
        t) TARGET="$OPTARG" ;;
        p) TRANSPORT="$OPTARG" ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage ;;
    esac
done
shift $((OPTIND - 1))

SIPSAK_MODE="-U"          # USRLOC mode — tells sipsak to send REGISTER
TRANSPORT_OPTS=""
TRANSPORT_TAG="UDP"
if [ "$TRANSPORT" = "tcp" ]; then
    TRANSPORT_OPTS="--transport=tcp"
    TRANSPORT_TAG="TCP"
fi

PASS=0
FAIL=0

pass() { echo "  ✓ $1"; ((PASS++)); }
fail() { echo "  ✗ $1"; ((FAIL++)); }

do_register() {
    local user="$1" expires="$2"
    sipsak $SIPSAK_MODE $TRANSPORT_OPTS \
        -s "sip:${user}@${TARGET}" \
        -C "sip:${user}@192.168.1.5" \
        -x "$expires" -vv -i 2>/dev/null || true
}

has_ok() {
    echo "$1" | grep -q "OK"
}

echo "=== REGISTER validation ($TARGET, transport=$TRANSPORT_TAG) ==="
echo ""

echo "--- register alice (expires=3600) ---"
log=$(do_register "alice" 3600)
if has_ok "$log"; then
    pass "register alice"
else
    fail "register alice"
    echo "$log" | sed 's/^/    /'
fi

echo ""
echo "--- unregister alice (expires=0) ---"
log=$(do_register "alice" 0)
if has_ok "$log"; then
    pass "unregister alice"
else
    fail "unregister alice"
    echo "$log" | sed 's/^/    /'
fi

echo ""
echo "--- concurrent: register bob + carol then unregister bob ---"
do_register "bob"   3600 >/dev/null
do_register "carol" 3600 >/dev/null
log=$(do_register "bob" 0)
if has_ok "$log"; then
    pass "unregister bob (carol stays)"
else
    fail "unregister bob"
    echo "$log" | sed 's/^/    /'
fi

echo ""
echo "=== results: ${PASS} passed, ${FAIL} failed (${TRANSPORT_TAG}) ==="
exit $FAIL
