#!/usr/bin/env bash
#
# run_edison.sh — register SIP client "edison" that answers calls,
#                 plays a .wav file, then hangs up.
#
# Prerequisites:
#   - trecd running (see scripts/run_trecd.sh)
#   - pjsua installed (brew install pjsua)
#   - sox installed for tone generation (brew install sox)
#
# Usage:
#   ./scripts/run_edison.sh [-t target] [-p proto] [-w wav_file] [-l local_port]
#
# Options:
#   -t target   Server address (default: 127.0.0.1:5061)
#   -p proto    SIP transport: udp or tcp (default: udp)
#   -w wav_file WAV file to play (default: generates a 440Hz tone)
#   -l port     Local SIP port (default: derived from target port + 1)
#   -h          Show this help and exit
#
set -euo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [-h] [-t target] [-p proto] [-w wav_file]

Register "edison" and answer incoming calls by playing a .wav file then hanging up.

Options:
  -t target   Server address (default: 127.0.0.1:5061)
  -p proto    SIP transport: udp or tcp (default: udp)
  -w wav_file WAV file to play (default: generates a 440Hz tone)
  -h          Show this help and exit
EOF
    exit 0
}

TARGET="127.0.0.1:5061"
PROTO="udp"
WAV_FILE=""
LOCAL_PORT=""

while getopts ":ht:p:w:l:" opt; do
    case "$opt" in
        h) usage ;;
        t) TARGET="$OPTARG" ;;
        p) PROTO="$OPTARG" ;;
        w) WAV_FILE="$OPTARG" ;;
        l) LOCAL_PORT="$OPTARG" ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage ;;
    esac
done
case "$PROTO" in
    udp|tcp) ;;
    *) echo "invalid protocol: $PROTO (use udp or tcp)" >&2; exit 1 ;;
esac
shift $((OPTIND - 1))

CLEANUP_FILES=()

cleanup() {
    for f in "${CLEANUP_FILES[@]}"; do
        rm -f "$f"
    done
}
trap cleanup EXIT

if [ -z "$WAV_FILE" ]; then
    WAV_FILE=$(mktemp /tmp/trec_edison_tone.XXXXXX.wav)
    CLEANUP_FILES+=("$WAV_FILE")
    echo "--- generating 440Hz tone ---"
    sox -n -b 16 -r 8000 -c 1 "$WAV_FILE" synth 10 sine 440 2>&1
fi

if [ ! -f "$WAV_FILE" ]; then
    echo "error: WAV file not found: $WAV_FILE" >&2
    exit 1
fi

if [ -z "$LOCAL_PORT" ]; then
    # Derive local port from target port to avoid conflict
    TARGET_PORT="${TARGET##*:}"
    LOCAL_PORT=$((TARGET_PORT + 1))
fi

SIP_PARAMS=""
[ "$PROTO" = "tcp" ] && SIP_PARAMS=";transport=tcp"

echo "Registering edison on ${TARGET} (${PROTO}) with local port ${LOCAL_PORT} ..."
echo "Press Ctrl-C to stop."
echo ""

pjsua \
    --rtp-port 16000 16099 \
    --id "sip:edison@127.0.0.1:${LOCAL_PORT}" \
    --local-port "$LOCAL_PORT" \
    --registrar "sip:${TARGET}${SIP_PARAMS}" \
    --realm "*" \
    --auto-answer 200 \
    --play-file "$WAV_FILE" \
    --auto-play \
    --auto-play-hangup \
    $([ "$PROTO" = "tcp" ] && echo "--no-udp")
