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

## Discovery

When a module connects, the core immediately sends an empty request (`{"id": "..."}`) to discover the module. The module must respond with:

```json
{"id": "...", "ok": true, "name": "my-module", "value": "...manual text..."}
```

The `"name"` field is **required**. The core uses it as the module's stable identifier. If a module with the same name is already connected, the core exits with an error.

After discovery, the core sends subsequent requests with `"op"` fields. A request with no `"op"` should return the manual (same as the discovery response).

## Sending Requests to the Core

If your module needs to send requests to the core (e.g., to dispatch incoming events), connect to the callback socket (`LOGOS_CB_SOCK`, default `/tmp/logos-cb.sock`) using the same wire format.

Include `"module": "my-module"` in every message you send to the core on the callback socket. Use the same name from your discovery response.

## That's It

Connect, receive the discovery request, respond with your name and manual, then handle subsequent requests. Stay connected — disconnecting unregisters the module.
