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
- **Circle.** Any active member can invite a device. A core adds a device to its member list only when an active member it already trusts signed that device's admission, and marks a member revoked only when the creator signed the revocation; cores exchange these proofs between themselves. A device ID is derived from its Ed25519 key, so a known key never changes. Every member holds the same symmetric circle key (AES-GCM), and the creator replaces it on every revocation (see [Key rotation](#key-rotation)).
- **What a member can still do.** Inviting is open to every member by design, so a member with a modified core can admit devices of its own, and they stay after that member is revoked unless the creator uses `share circle revoke --with-invitees`. Each admission records who signed it (`share circle list`).
- **Live session.** Circle membership does not open a terminal. The host core only serves a session to a device the session owner shared it with (an invite, or a terminal card posted in a chat). See [Live terminals](#live-terminals).
- **Local dashboard.** The dashboard and its APIs bind to `127.0.0.1` by default. State-changing APIs reject non-loopback callers, cross-site fetches, and foreign `Origin` headers.

### What the circle key protects

The circle key keeps share-listener batches and MQTT payloads unreadable to devices **outside** the circle. The MQTT broker also requires TLS 1.3 with a client certificate from an active member.

The circle key does **not** separate members from each other:

- **Chat.** Messages are signed by the sender and sealed with the circle key. Every member's core receives every chat topic, and direct messages and custom groups are filtered by the receiving core. A member that runs a modified core can read direct and group messages it was not addressed to. Treat circle chat as *encrypted circle messaging*, not end-to-end encryption between two people.
- **No forward secrecy inside a key epoch.** The circle key lasts until the next rotation. There is no ratchet, so a leaked key exposes the traffic captured while it was current. This is not equivalent to the Signal protocol.

### Key rotation

- Every `share circle revoke` rotates the circle key; `share circle rekey` rotates it on demand. Only the creator rotates.
- Each device derives a key-agreement key (HPKE with ML-KEM-768 + X25519) from its Ed25519 identity and publishes it signed. The creator seals the new key to each remaining member separately and signs the update. A revoked device gets no entry, so it cannot read anything sealed after the rotation.
- What a revoked device keeps: traffic it captured before the rotation, and traffic from members still on the previous key. Receivers accept the previous key for 24 hours so messages in flight still arrive; a member that has not received the new key yet keeps sealing with the old one until it syncs, which takes about a minute while its mesh is open.
- Key updates travel through a separate `keys` exchange that is not sealed with the circle key, because a member that missed a rotation no longer shares one. It carries device IDs, public key-agreement keys, entries each sealed to one device, and signatures. A network observer can see which devices are in the circle.
- A member whose core predates rotation publishes no key-agreement key and is left without the new key. The revoke and rekey commands name such members; each gets the current key once its core upgrades and syncs with the creator. Upgrade every member before revoking anyone.

### Live terminals

- The host checks, on every remote operation (metadata, control requests, stream), that the requesting device is an active circle member **and** holds a share for that session. The owner and the host device always pass.
- Only the session owner creates shares: an invite to one device, or a terminal card posted in a direct conversation (the recipient), a custom group (its members at that moment), or the circle channel (active members at that moment). A card's shares are granted only after the card is saved, all at once; if either step fails, the post fails and nobody gains access.
- Removing a participant or revoking a circle member deletes its shares and hands any control lease back to the owner. New requests from that device are refused at once. After a key rotation, remote links still sealed with the old key close within 15 seconds and viewers reconnect with the new one.
- Streams that are already open are re-admitted every 15 seconds and closed when the device is no longer admitted. Removing a participant from the host's dashboard closes its streams at once. Private mode, and `share circle revoke` run from the CLI, reach open streams at the next check: a revoked viewer can keep seeing output for up to 15 seconds.
- Viewing never implies typing. Input and resize require the control lease, granted by the owner, with a monotonic epoch that rejects stale controllers.
- Sessions shared before 2.0.2 relied on circle membership alone. After upgrading the host, share them again.

### Calls

WebRTC media flows peer to peer over DTLS-SRTP. Signaling uses the circle transport. With a TURN server configured, the relay sees encrypted media and connection metadata.

### Out of scope

- A compromised device that is still an active member.
- Exposing the dashboard beyond loopback (`OVERDRIVE_SHARE_UI` on a non-loopback address).
- Traffic analysis (who talks to whom, when, and how much).
