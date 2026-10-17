# Security Policy

## Supported Versions

| Version | Supported |
| ------- | --------- |
| 2.0.x   | Yes       |
| < 2.0   | No        |

## Reporting a Vulnerability

If you discover a security issue in Overdrive, please report it responsibly:

- **Email:** [contact@gfrancodev.com](mailto:contact@gfrancodev.com)
- **GitHub:** [Open a private security advisory](https://github.com/gfrancodev/overdrive/security/advisories/new) on the repository.

## What to Include

Please include as much of the following as you can:

- A clear description of the issue and where it occurs
- Steps to reproduce, including version, OS, and runtime (Cursor, Claude Code, Codex, etc.)
- Impact assessment and any proof-of-concept you are comfortable sharing
- Suggested fix or mitigation, if you have one

Do not disclose the issue publicly until we have had a chance to review and address it.

We aim to acknowledge reports within a few business days and will work with you on remediation and disclosure timing.

## Mesh Threat Model

This section describes what the share mesh (`share listen`, circles, dashboard, chat, calls, and live terminals) protects and what it does not. It applies to 2.0.x.

### Trust boundaries

- **Device identity.** Each core has an Ed25519 key. Peer requests (sync, catalog, terminal) are signed, and a tampered request is rejected. Terminal requests are also rejected outside a two-minute timestamp window.
- **Circle.** Any active member can invite a device, and the member list spreads between members during sync. A device ID is derived from its Ed25519 key, so a peer can add devices to that list but cannot change the key of a device already on it. Only the creator can run `share circle revoke`, but the member list is not yet signed end to end: an active member running a modified core can add devices, or mark members revoked, on the peers that sync with it. Every member holds the same symmetric circle key (AES-GCM).
- **Live session.** Circle membership does not open a terminal. The host core only serves a session to a device the session owner shared it with (an invite, or a terminal card posted in a chat). See [Live terminals](#live-terminals).
- **Local dashboard.** The dashboard and its APIs bind to `127.0.0.1` by default. State-changing APIs reject non-loopback callers, cross-site fetches, and foreign `Origin` headers.

### What the circle key protects

The circle key keeps share-listener batches and MQTT payloads unreadable to devices **outside** the circle. The MQTT broker also requires TLS 1.3 with a client certificate from an active member.

The circle key does **not** separate members from each other:

- **Chat.** Messages are signed by the sender and sealed with the circle key. Every member's core receives every chat topic, and direct messages and custom groups are filtered by the receiving core. A member that runs a modified core can read direct and group messages it was not addressed to. Treat circle chat as *encrypted circle messaging*, not end-to-end encryption between two people.
- **No forward secrecy.** The circle key is long-lived. There is no ratchet, so a leaked key exposes past captured traffic. This is not equivalent to the Signal protocol.
- **Revocation does not rotate the key.** A revoked device is rejected by identity checks (broker mTLS, signed requests, live-session admission), but it still knows the circle key. If it can capture traffic on the network path, it can decrypt share-listener batches. Create a new circle when that matters.

### Live terminals

- The host checks, on every remote operation (metadata, control requests, stream), that the requesting device is an active circle member **and** holds a share for that session. The owner and the host device always pass.
- Only the session owner creates shares: an invite to one device, or a terminal card posted in a direct conversation (the recipient), a custom group (its members at that moment), or the circle channel (active members at that moment). A card's shares are granted only after the card is saved, all at once; if either step fails, the post fails and nobody gains access.
- Removing a participant or revoking a circle member deletes its shares and hands any control lease back to the owner. New requests from that device are refused at once.
- Streams that are already open are re-admitted every 15 seconds and closed when the device is no longer admitted. Removing a participant from the host's dashboard closes its streams at once. Private mode, and `share circle revoke` run from the CLI, reach open streams at the next check: a revoked viewer can keep seeing output for up to 15 seconds.
- Viewing never implies typing. Input and resize require the control lease, granted by the owner, with a monotonic epoch that rejects stale controllers.
- Sessions shared before 2.0.2 relied on circle membership alone. After upgrading the host, share them again.

### Calls

WebRTC media flows peer to peer over DTLS-SRTP. Signaling uses the circle transport. With a TURN server configured, the relay sees encrypted media and connection metadata.

### Out of scope

- A compromised device that is still an active member.
- Exposing the dashboard beyond loopback (`OVERDRIVE_SHARE_UI` on a non-loopback address).
- Traffic analysis (who talks to whom, when, and how much).
