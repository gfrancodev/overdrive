// Circle settings in the mesh dashboard: the folders this device shares, the members and, on the creator,
// removing one. A removal shows what it does before it happens; the Core computes who loses access.

function folderLabel(path) {
  const parts = String(path || "").split(/[\\/]+/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : String(path || "");
}

function memberName(member) {
  return String(member?.hostname || member?.device_id || "Device");
}

function circleMemberNames(circle) {
  const names = {};
  (circle?.members || []).forEach((m) => { names[m.device_id] = memberName(m); });
  return names;
}

function nameList(ids, names) {
  const list = (ids || []).map((id) => names[id] || id);
  if (list.length <= 1) return list.join("");
  return list.slice(0, -1).join(", ") + " and " + list[list.length - 1];
}

// memberNotes is the secondary line of one member row.
function memberNotes(member, circle) {
  const names = circleMemberNames(circle);
  const notes = [];
  if (member.self) notes.push("This device");
  if (member.device_id === circle?.creator_device_id) notes.push("Creator");
  else if (member.invited_by) notes.push("Invited by " + (names[member.invited_by] || member.invited_by));
  if (member.revoked) notes.push("Removed");
  else if (!member.receives_keys) notes.push("Needs an Overdrive update to get new keys");
  return notes.join(" · ");
}

function canRemoveMember(member, circle) {
  return Boolean(circle?.is_creator && !member.self && !member.revoked && member.device_id !== circle.creator_device_id);
}

function formatWhen(iso) {
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return String(iso || "");
  return when.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function countLabel(n, one, many) {
  return n + " " + (n === 1 ? one : many);
}

// keyStatusText says which circle key is in use and how long devices on the previous one keep working.
function keyStatusText(circle, now = Date.now()) {
  const epoch = Number(circle?.key_epoch) || 0;
  if (!epoch) return "The circle still uses its first key. It changes every time a device is removed.";
  let text = "The key changed " + countLabel(epoch, "time", "times");
  if (circle.key_rotated_at) text += ", last on " + formatWhen(circle.key_rotated_at);
  text += ".";
  const until = Date.parse(circle.previous_key_until || "");
  if (!Number.isNaN(until) && until > now) {
    text += " Devices still on the previous key keep working until " + formatWhen(circle.previous_key_until) + ".";
  }
  return text;
}

// revokeImpact turns the Core's preview of a removal into what the person is about to do.
function revokeImpact(plan, circle, withInvitees) {
  const names = circleMemberNames(circle);
  const target = names[plan.targets?.[0]] || plan.targets?.[0] || "This device";
  const invitees = plan.invitees || [];
  const lines = [];
  if (withInvitees && invitees.length) {
    lines.push(target + " and the " + countLabel(invitees.length, "device", "devices") + " it invited (" + nameList(invitees, names) + ") lose access now: no new chat, calls, terminals or shared experience.");
  } else {
    lines.push(target + " loses access now: no new chat, calls, terminals or shared experience.");
  }
  lines.push("Live terminals shared with " + (withInvitees && invitees.length ? "them" : "it") + " are closed.");
  lines.push("The circle key changes. Devices online get the new key right away; the others when they reconnect.");
  if (!withInvitees && invitees.length) {
    lines.push("Devices " + target + " invited stay in the circle: " + nameList(invitees, names) + ".");
  }
  if (plan.cut_off?.length) {
    lines.push(nameList(plan.cut_off, names) + " run" + (plan.cut_off.length === 1 ? "s" : "") + " an Overdrive without key rotation and lose" + (plan.cut_off.length === 1 ? "s" : "") + " access until updated.");
  }
  lines.push("What " + target + " already received stays on it: removing a device cannot erase copies.");
  return { title: "Remove " + target + "?", lines, inviteeCount: invitees.length };
}

async function readCircleJSON(res) {
  try {
    return await res.json();
  } catch {
    return {};
  }
}

function createCircleSettings({ activeCircleId, listRow, icon, onChange } = {}) {
  const byId = (id) => document.getElementById(id);
  let view = null;
  let removing = null;
  let settingsParked = false;

  function circleBase() {
    const id = activeCircleId ? activeCircleId() : "";
    return id ? "/api/circles/" + encodeURIComponent(id) : "";
  }
  function setStatus(id, text) {
    const node = byId(id);
    if (node) node.textContent = text || "";
  }
  function rowButton(label, aria, className, onClick) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "row-action" + (className ? " " + className : "");
    button.textContent = label;
    button.setAttribute("aria-label", aria);
    button.addEventListener("click", (ev) => {
      ev.stopPropagation();
      onClick();
    });
    return button;
  }

  function renderFolders() {
    const box = byId("circleFolders");
    const hints = byId("folderSuggestionRows");
    if (!box || !hints) return;
    box.replaceChildren();
    hints.replaceChildren();
    const folders = view?.folders || [];
    if (!folders.length) box.appendChild(listRow({ title: "No folder shared yet", detail: "Nothing from this device reaches the circle until you share a folder.", quiet: true, wrap: true }));
    folders.forEach((folder) => {
      box.appendChild(listRow({
        lead: icon("iconFolder"),
        title: folder.name || folderLabel(folder.path),
        detail: folder.exists ? folder.path : folder.path + " · not found on this device",
        aside: [rowButton("Stop sharing", "Stop sharing " + folder.path, "", () => { removeFolder(folder.path).catch(() => {}); })],
      }));
    });
    const suggestions = view?.suggestions || [];
    hints.hidden = suggestions.length === 0;
    suggestions.forEach((folder) => {
      hints.appendChild(listRow({
        lead: icon("iconFolder"),
        title: folder.name || folderLabel(folder.path),
        detail: folder.path,
        aside: [rowButton("Share", "Share " + folder.path, "", () => { addFolder(folder.path).catch(() => {}); })],
      }));
    });
  }
  function renderMembers() {
    const box = byId("circleMembers");
    if (!box) return;
    box.replaceChildren();
    const circle = view?.circle;
    (circle?.members || []).forEach((member) => {
      const aside = canRemoveMember(member, circle)
        ? [rowButton("Remove", "Remove " + memberName(member), "danger", () => { previewRemoval(member.device_id, false).catch(() => {}); })]
        : [];
      box.appendChild(listRow({ title: memberName(member), detail: memberNotes(member, circle), aside, wrap: true }));
    });
    const key = byId("circleKey");
    if (key) key.textContent = circle ? keyStatusText(circle) : "";
    const who = byId("circleRemoveHint");
    if (who) {
      const creator = (circle?.members || []).find((m) => m.device_id === circle?.creator_device_id);
      who.hidden = !circle || circle.is_creator;
      who.textContent = "Only " + (creator ? memberName(creator) : "the circle creator") + " can remove devices from this circle.";
    }
  }
  function render() {
    const section = byId("circleManage");
    if (section) section.hidden = !view?.circle;
    renderFolders();
    renderMembers();
  }

  async function refresh() {
    const base = circleBase();
    if (!base) {
      view = null;
      render();
      return;
    }
    const res = await fetch(base + "/folders", { cache: "no-store" });
    if (!res.ok) return;
    view = await res.json();
    render();
  }
  async function applyFolderResponse(res, done) {
    const body = await readCircleJSON(res);
    if (!res.ok) {
      setStatus("folderStatus", body.error || "Could not update the shared folders.");
      return;
    }
    view = body;
    setStatus("folderStatus", done);
    render();
    if (onChange) onChange();
  }
  async function addFolder(path) {
    const base = circleBase();
    const value = String(path || "").trim();
    if (!base) return;
    if (!value) {
      setStatus("folderStatus", "Type the folder's full path or pick a project below.");
      return;
    }
    const res = await fetch(base + "/folders", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path: value }) });
    await applyFolderResponse(res, "Sharing " + folderLabel(value) + " with the circle.");
    const field = byId("folderPath");
    if (res.ok && field) field.value = "";
  }
  async function removeFolder(path) {
    const base = circleBase();
    if (!base) return;
    const res = await fetch(base + "/folders?path=" + encodeURIComponent(path), { method: "DELETE" });
    await applyFolderResponse(res, "Stopped sharing " + folderLabel(path) + ". Devices keep what they already received.");
  }

  function renderRemoval(plan) {
    const impact = revokeImpact(plan, view?.circle, removing.withInvitees);
    byId("revokeTitle").textContent = impact.title;
    const list = byId("revokeImpact");
    list.replaceChildren();
    impact.lines.forEach((line) => {
      const item = document.createElement("li");
      item.textContent = line;
      list.appendChild(item);
    });
    const field = byId("revokeInviteesField");
    field.hidden = impact.inviteeCount === 0;
    byId("revokeInviteesText").textContent = "Also remove the " + countLabel(impact.inviteeCount, "device", "devices") + " it invited";
    byId("revokeInvitees").checked = removing.withInvitees;
    byId("revokeConfirm").disabled = false;
    // The confirmation takes the settings sheet's place, and gives it back when it closes.
    const settings = byId("settings");
    if (settings && !settings.hidden) {
      settings.hidden = true;
      settingsParked = true;
    }
    byId("revoke").hidden = false;
  }
  async function previewRemoval(deviceId, withInvitees) {
    const base = circleBase();
    if (!base) return;
    removing = { deviceId, withInvitees };
    setStatus("revokeStatus", "");
    const res = await fetch(base + "/members/" + encodeURIComponent(deviceId) + "/revoke" + (withInvitees ? "?with_invitees=1" : ""), { cache: "no-store" });
    const body = await readCircleJSON(res);
    if (!res.ok) {
      setStatus("folderStatus", body.error || "Could not prepare the removal.");
      removing = null;
      return;
    }
    renderRemoval(body);
  }
  function closeRemoval(backToSettings = true) {
    removing = null;
    const panel = byId("revoke");
    if (panel) panel.hidden = true;
    const settings = byId("settings");
    if (settingsParked && backToSettings && settings) settings.hidden = false;
    settingsParked = false;
  }
  async function confirmRemoval() {
    const base = circleBase();
    if (!base || !removing) return;
    byId("revokeConfirm").disabled = true;
    const res = await fetch(base + "/members/" + encodeURIComponent(removing.deviceId) + "/revoke", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ with_invitees: removing.withInvitees }),
    });
    const body = await readCircleJSON(res);
    if (!res.ok) {
      byId("revokeConfirm").disabled = false;
      setStatus("revokeStatus", body.error || "Could not remove the device.");
      return;
    }
    const names = circleMemberNames(view?.circle);
    closeRemoval();
    let done = "Removed " + nameList(body.revoked, names) + ". The circle key changed.";
    if (body.cut_off?.length) done += " " + nameList(body.cut_off, names) + " get the new key once updated.";
    setStatus("folderStatus", done);
    await refresh();
    if (onChange) onChange();
  }

  byId("folderAddButton")?.addEventListener("click", () => { addFolder(byId("folderPath")?.value).catch(() => {}); });
  byId("folderPath")?.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") addFolder(ev.target.value).catch(() => {});
  });
  byId("revokeInvitees")?.addEventListener("change", (ev) => {
    if (removing) previewRemoval(removing.deviceId, ev.target.checked).catch(() => {});
  });
  byId("revokeConfirm")?.addEventListener("click", () => { confirmRemoval().catch(() => {}); });
  byId("revokeCancel")?.addEventListener("click", () => closeRemoval());
  byId("revokeClose")?.addEventListener("click", () => closeRemoval());

  return { refresh, closeRemoval };
}

export {
  canRemoveMember,
  createCircleSettings,
  folderLabel,
  keyStatusText,
  memberNotes,
  revokeImpact
};
