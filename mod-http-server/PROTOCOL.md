# Logos Module Builders Guide

A module is a process that connects to the logos core over a unix domain socket and responds to requests.

## Connecting

Connect to the module socket (`LOGOS_MOD_SOCK`, default `/tmp/logos-mod.sock`) as a unix stream socket. The core must be running first.

## Wire Format

Every message is:

1. **4 bytes** — message length as a big-endian uint32
2. **N bytes** — JSON object (UTF-8)

No framing, no headers, no negotiation.

## Messages

Every message is a JSON object with an `"id"` field (string).

**Request** — has an `"id"` and any other fields:

```json
{"id": "r1", "op": "now"}
{"id": "r2", "op": "read-file", "path": "/tmp/foo.txt"}
{"id": "r3"}
```

**Response** — has the matching `"id"`, plus `"ok"` and either `"value"` or `"error"`:

```json
{"id": "r1", "ok": true, "value": 1709078400}
{"id": "r2", "ok": false, "error": "file not found"}
```

## Empty Request

A request with no fields beyond `"id"` should be answered with a **manual** — a description of the module and how to use it. Format is up to you.

## Sending Requests to the Core

If your module needs to send requests to the core, connect to the callback socket (`LOGOS_CB_SOCK`, default `/tmp/logos-cb.sock`) using the same wire format.

The core receives requests from multiple modules on this socket. To identify yourself, generate a UUID at startup and include it as `"module"` in every message you send to the core. Document in your manual what the UUID represents. UUIDs are important here — they guarantee no collisions between modules.

## That's It

Connect, receive requests, send responses. Stay connected — disconnecting unregisters the module.
