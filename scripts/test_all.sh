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

cleanup() {
    echo ""
    echo "=== stopping trecd ==="
    "$ROOT/scripts/run_trecd.sh" stop
}
trap cleanup EXIT

"$ROOT/scripts/run_trecd.sh" start "$TARGET"

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
if [ "$EXIT" -eq 0 ]; then
    echo " ALL TESTS PASSED"
else
    echo " ${EXIT} test suite(s) failed"
fi
echo "=================================================================="
exit $EXIT
