# Cross-machine runbook (2.3)

The automated suites run every core on one computer, over loopback. This runbook checks what only real machines
show: installing from a bundle, mDNS between hosts, firewalls, the network choice, calls, and a removal seen from
three devices. Run it before tagging a release that changes pairing, the mesh, or installation, and record the
results in `docs/runbook-results/<version>-<date>.md` (copy the table at the end).

## Setup

- Three computers on the same LAN: **A**, **B** and **C**. Use at least two operating systems; one Windows
  machine catches firewall and path problems the others hide.
- The release bundles, `overdrive-<os>-<arch>.zip`, for each machine. No Go, Rust or Docker on any of them.
- The same test repository cloned on each machine, with the same `origin` remote. Any repository works.
- A stopwatch for the first collaboration. Start it at step 1 on A and stop it at step 7.

Diagnostics on each machine, when a step asks for them:

```bash
curl -s http://127.0.0.1:7742/api/diagnostics > diag-$(hostname)-<step>.json   # Windows: curl.exe, $env:COMPUTERNAME
```

## Steps

| # | Where | Do | Expect |
|---|---|---|---|
| 1 | all | Unzip the bundle and run `install.sh` (`install.ps1` on Windows). | Install finishes without a toolchain; the tray appears and the Core starts. The dashboard opens from the tray. |
| 2 | all | Dashboard → network icon in the menu bar → **Allow devices on this network**. Accept the firewall prompt if the system shows one. | The Connection sheet says "Devices on this network connect to `<LAN IP>`:7741". |
| 3 | A | **Get started** → create a circle. | The next step is "Pair another device". |
| 4 | B | **Nearby** → **Pair** on A. Compare the six digits on both screens. On A pick the circle and confirm; then confirm on B. | Both show the same code; B joins. Typing the wrong circle or cancelling on either side adds nothing. |
| 5 | C | **Nearby** → **Pair** on **B** (not A). Confirm on both. | C joins through B. Within a minute A lists C in Circle settings, "Invited by B". |
| 6 | all | Circle settings → share the test repository folder. | The folder is listed; Get started says everything is set up. |
| 7 | all | Open the Connection sheet. | Chat, Calls, Experience and Terminals all **Connected**, each other device "Answers". Stop the stopwatch: first collaboration should take five minutes or less. Save diagnostics (`step7`). |
| 8 | A, C | On A, inside the repository: `overdrive-runtime record --kind lesson --subject "runbook <date>" --content "Lesson from A" --source verified_execution`. On C: `overdrive-runtime session-start --cwd .` then `overdrive-runtime recall --query "runbook <date>"`. | C's recall lists A's lesson under `peer_memories`. |
| 9 | A, B, C | Messages → send to the whole circle from A; send a direct message from B to C. | Every circle member gets A's message; only C gets B's. |
| 10 | A, B | Call B from A (voice, then video). | Audio and video both ways on the LAN. |
| 11 | A, B | Terminal on A → share the session with B → B asks for control, A grants it, B runs `echo ok`. | B sees output live and can type after A grants control. |
| 12 | all | Unplug the internet at the router (keep the LAN). Reload every dashboard; repeat 9 and 11. | The dashboard loads with the globe; chat and terminals still work; the Connection sheet stays Connected. Plug the internet back. |
| 13 | A | Open a terminal and start `top` (or `ping -t 127.0.0.1` on Windows). Tray → **Stop Overdrive**, then **Start Overdrive**. | The terminal is still there with `top` running after the Core restarts. |
| 14 | A | Circle settings → **Remove** on C. Read the preview, then confirm. | The preview names C, says the key changes and nobody is cut off. Afterwards A shows "The key changed 1 time". |
| 15 | B | Circle settings. | Within seconds: key changed 1 time, C marked Removed. |
| 16 | C | Send a circle message; on C run `session-start` then `recall` for a new lesson recorded on A after step 14. | A and B never get C's message; C does not get the new lesson. |
| 17 | B | Connection → **Accept connections only from this computer**. On A, open Connection. | Within ten seconds A shows Experience and Terminals offline, and B "Accepts connections only from itself" with the fix to run on B. Chat stays connected. Allow the network on B again. |
| 18 | all | Save diagnostics (`step18`) and search them for the circle key: `grep -c group_key diag-*.json`, and for the repository folder name. | No match in any file. |

Optional, for the adversarial pass the security roadmap asks for:

- From a fourth device that is not in the circle, start a pairing with A and cancel on A: nothing is added on
  either side, and A's Nearby keeps working.
- Copy C's `~/.overdrive/share/circles/<id>.json` from before step 14 onto a fourth device and try to sync
  (`session-start`) against A: A answers nothing; its log shows no import.

## Recording

```markdown
# Cross-machine run: <version>, <date>

| Machine | OS / arch | Install | Notes |
|---|---|---|---|
| A | | | |
| B | | | |
| C | | | |

First collaboration (steps 1–7): <mm:ss>

| Step | Result (pass / fail) | Notes, with diagnostics file when it failed |
|---|---|---|
| 1 | | |
| … | | |
| 18 | | |
```

A failed step blocks the release unless the release notes name it as a known issue.
