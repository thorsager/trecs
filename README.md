# TRECS

Project for basic telephony (SIP for now) recording.

## Protocol library

The `proto` package provides wire-format parsing and serialization for SIP, SDP, RTP, and RTCP. See [proto/PROTO.md](proto/PROTO.md) for usage examples.

## Echo test

The echo test validates real-time audio loopback over RTP: a SIP client calls the server, negotiates a media session (early-offer or delayed-offer), sends an RTP packet, and verifies the server echoes the exact payload back. The flow is described in detail in [internal/media/media_test.go](internal/media/media_test.go).

### Prerequisites

- Go 1.26+
- `sipsak` (optional): `brew install sipsak`
- `pjsua` (optional, command-line SIP phone): see pjsip.org

### Quick: automated integration test

```bash
go test -v -run TestEcho -timeout 60s ./internal/media/
```

This runs `TestEchoEarlyOffer` and `TestEchoDelayedOffer`, testing both offer modes in-process, no external tools needed.

### Automated end-to-end test with sox + pjsua

[`scripts/test_echo.sh`](scripts/test_echo.sh) generates a sine tone with
`sox`, calls the echo service via `pjsua`, and verifies the echoed audio
is non-empty with matching duration. It supports both UDP and TCP SIP
transport (`-p udp|tcp`).

```bash
# Quick smoke test (auto-starts trecd, runs for 3 seconds):
./scripts/test_echo.sh -s -t 127.0.0.1:5061

# TCP transport:
./scripts/test_echo.sh -s -t 127.0.0.1:5061 -p tcp

# Custom duration:
./scripts/test_echo.sh -s -t 127.0.0.1:5061 -d 10

# Point at an already-running trecd:
./scripts/test_echo.sh -t 127.0.0.1:5063
```

Prerequisites: `pjsua` (`brew install pjproject`), `sox` (`brew install sox`).

For a fully manual walkthrough (what the script automates), see the
[pjsua invocation details in the script](scripts/test_echo.sh).

### Run all tests

```bash
go test ./...
```

## B2BUA (Back-to-Back User Agent)

T-REC can bridge two SIP legs together. A typical flow: Alice calls a
registered user (Bob), and T-REC forwards the call to Bob's registered
contact. Registration is handled by the built-in registrar.

### Register Bob with pjsua

This registers Bob via TCP and auto-answers incoming calls with a
greeting, then hangs up after the file finishes (or 30s timeout):

```bash
pjsua --id "sip:bob@example.com" \
      --registrar "sip:127.0.0.1:5061" \
      --local-port 5062 --no-udp \
      --realm "*" --username bob --password secret \
      --auto-answer 200 --duration 30 \
      --play-file greeting.wav --auto-play --auto-play-hangup
```

Breakdown:
- `--registrar` — points at TRECS' SIP address
- `--local-port 5062` — avoids conflicting with T-REC on 5061
- `--no-udp` — forces TCP transport only
- `--auto-answer 200` — automatically answers inbound calls
- `--duration 30` — hangs up after 30s (safety net)
- `--play-file / --auto-play / --auto-play-hangup` — plays a WAV to the
  caller and hangs up when it ends

## Example configuration

Example config files are in [`docs/examples/`](docs/examples/).

### Users

[`docs/examples/users.json`](docs/examples/users.json) defines two users with
Digest authentication (MD5, realm `127.0.0.1`):

| User  | Password   | AOR                       |
|-------|------------|---------------------------|
| alice | `secret`   | `sip:alice@127.0.0.1`    |
| bob   | `password` | `sip:bob@127.0.0.1`      |

The server can be started with auth enabled:

```bash
trecsd --addr :5060 --auth-users docs/examples/users.json
```

This enables Digest authentication for both REGISTER and INVITE/BYE (proxy
auth). Registering with a matching password grants binding to the user's AOR.

By default, a client gets up to three consecutive auth attempts before the
server responds with `403 Forbidden`. Use `--auth-max-failed-attempts` to
change the threshold (range 1–10).

### Dialplan

[`docs/examples/dialplan.json`](docs/examples/dialplan.json) maps extensions
to actions:

| Extension   | Action    |
|-------------|-----------|
| `echo`      | echo      |
| `echo_test` | echo      |

The echo action loops back any RTP audio received. Start with a dialplan:

```bash
trecsd --addr :5060 --dialplan docs/examples/dialplan.json
```

Both flags can be combined:

```bash
trecsd --addr :5060 --dialplan docs/examples/dialplan.json --auth-users docs/examples/users.json
```
