# TRECS

Project for basic telephony (SIP for now) recording.

## Protocol library

The `proto` package provides wire-format parsing and serialization for SIP,
SDP, RTP, and RTCP. See [proto/PROTO.md](proto/PROTO.md) for usage examples.

## Echo test

The echo test validates real-time audio loopback over RTP: a SIP client calls
the server, negotiates a media session (early-offer or delayed-offer), sends
an RTP packet, and verifies the server echoes the exact payload back. The flow
is described in detail in [internal/media/media_test.go](internal/media/media_test.go).

### Prerequisites

- Go 1.26+
- `sipsak` (optional): `brew install sipsak`
- `pjsua` (optional, command-line SIP phone): see pjsip.org

### Quick: automated integration test

```bash
go test -v -run TestEcho -timeout 60s ./internal/media/
```

This runs `TestEchoEarlyOffer` and `TestEchoDelayedOffer`, testing both offer
modes in-process, no external tools needed.

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
| alice | `secret`   | `sip:alice@127.0.0.1`     |
| bob   | `password` | `sip:bob@127.0.0.1`       |

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

### Trunks

[`docs/examples/trunks.json`](docs/examples/trunks.json) defines SIP trunk
connections to external providers or peer PBXes:

```bash
trecsd --addr :5060 --trunks docs/examples/trunks.json
```

#### Trunk types

| Type | Description |
|------|-------------|
| `static` | Direct IP peering. The PBX trusts the peer by source IP (see `trusted_ips`). No REGISTER exchange. |
| `registration` | The PBX authenticates to the peer with Digest credentials and maintains a registration binding. |

#### Common fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Unique trunk identifier |
| `host` | string | required | Peer hostname or IP address |
| `port` | int | required | Peer SIP port |
| `transport` | string | `udp` | SIP transport (`udp` or `tcp`) |
| `max_channels` | int | `0` (unlimited) | Max concurrent calls. Exceeding returns `503 Service Unavailable`. |
| `caller_id` | string | `""` | Sets `P-Asserted-Identity` on outbound trunk INVITEs. The PAI contains `<sip:<caller_id>@<server_ip>>`. |
| `strip_headers` | [string] | `[]` | List of SIP header names to remove from outbound trunk INVITEs (e.g., internal headers like `X-Extension`). Headers are matched case-insensitively per RFC 3261. |
| `session_expires_sec` | int | `0` (disabled) | Enables RFC 4028 session timer. When non-zero, adds `Session-Expires: <N>;refresher=uac` to outbound INVITEs. If the timer expires without a BYE from the peer, the server tears down the call, sends BYE to the caller, and releases the trunk channel. |

#### Registration trunk fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `auth_user` | string | required | Username for Digest authentication during REGISTER |
| `auth_password` | string | required | Password for Digest authentication |
| `realm` | string | peer host | SIP realm for Digest authentication |
| `register_uri` | string | auto | Override the registration target URI (default: `sip:<auth_user>@<host>`) |

#### Static trunk fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trusted_ips` | [string] | `[]` | CIDR ranges (e.g. `["10.0.1.0/24"]`) that are trusted without proxy authentication. When an incoming INVITE matches a static trunk's trusted range, proxy auth is skipped. |

#### Outbound Routes

Routes match calling-party digits and dispatch calls to a specific trunk.
First-match-wins (config order).

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Route identifier |
| `pattern` | string | required | Go regexp matched against the dialled user part of the Request-URI |
| `trunk` | string | required | Target trunk name (must match a trunk's `name`) |
| `strip_digits` | int | `0` | Number of leading digits to strip from the dialled number before forwarding |
| `prefix` | string | `""` | Digits to prepend to the dialled number before forwarding (applied after `strip_digits`) |

#### Example

```json
{
  "trunks": [
    {
      "name": "office-pbx",
      "type": "static",
      "host": "10.0.1.50",
      "port": 5061,
      "transport": "tcp",
      "trusted_ips": ["10.0.1.0/24"],
      "max_channels": 20,
      "caller_id": "pbx-main",
      "session_expires_sec": 1800,
      "strip_headers": ["X-Extension"]
    }
  ],
  "outbound_routes": [
    {
      "name": "local",
      "pattern": "^\\d{3,5}$",
      "trunk": "office-pbx"
    }
  ]
}
```

#### Inbound trust

When an INVITE arrives from a source IP that matches a static trunk's
`trusted_ips`, proxy authentication (Digest auth) is bypassed. The call
proceeds directly to the dialplan or registrar lookup. This is configured
per-trunk and does not apply to registration-type trunks.
