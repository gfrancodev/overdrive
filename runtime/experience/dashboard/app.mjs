    const uiConfig = globalThis.OVERDRIVE_UI || { preset: "orbit", title: "Overdrive", logo: "/logo.png" };
    applyShareTheme(uiConfig);
    if ((uiConfig.preset || "orbit") === "enterprise") {
      bootEnterprise();
      globalThis.__odMessagesReady = true;
    } else {
      let orbitThree = null;
      try {
        orbitThree = await import("three");
      } catch (err) {
        globalThis.console?.debug?.(err);
        globalThis.__odBootError = String(err && err.stack || err);
      }
      try {
        await bootOrbit(orbitThree);
        globalThis.__odMessagesReady = true;
      } catch (err) {
        globalThis.__odBootError = String(err && err.stack || err);
        globalThis.console?.debug?.(err);
      }
    }

    function applyShareTheme(ui) {
      const preset = ui.preset === "enterprise" ? "enterprise" : "orbit";
      document.documentElement.dataset.preset = preset;
      const title = document.getElementById("barTitle");
      if (title && ui.title) title.textContent = String(ui.title).slice(0, 40);
      if (ui.logo) {
        document.querySelectorAll(".menubar .mark, #circleApp .mark").forEach((logo) => logo.setAttribute("src", ui.logo));
      }
      const allowed = ["ink", "sea", "foam", "cyan", "live", "mute"];
      const tokens = ui.tokens || {};
      allowed.forEach((key) => {
        const value = tokens[key];
        if (typeof value === "string" && /^#[0-9a-fA-F]{3,8}$/.test(value)) {
          document.documentElement.style.setProperty("--" + key, value);
        }
      });
    }

    function isValidIanaTimeZone(timeZone) {
      if (!timeZone || typeof timeZone !== "string") return false;
      try {
        Intl.DateTimeFormat(undefined, { timeZone });
        return true;
      } catch {
        return false;
      }
    }

    function clipDashboardText(text) {
      const value = String(text || "");
      return value.length > 96 ? value.slice(0, 96) : value;
    }

    function peerPresenceState(peer) {
      if (peer.revoked) return "revoked";
      return peer.endpoint ? "online" : "offline";
    }

    function createMenubarTimeZoneStore(opts) {
      const storageKey = opts.storageKey || "overdriveMenubarTimeZone";
      const dateLocale = opts.dateLocale || "en-US";
      const fallbackZones = opts.fallbackZones || ["UTC"];
      function systemTimeZone() {
        try {
          const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
          if (tz && isValidIanaTimeZone(tz)) return tz;
        } catch { /* unsupported */ }
        return fallbackZones[0] || "UTC";
      }
      function read() {
        try {
          const raw = localStorage.getItem(storageKey);
          if (raw && typeof raw === "string" && isValidIanaTimeZone(raw)) return raw;
        } catch { /* quota / private mode */ }
        return systemTimeZone();
      }
      function write(timeZone) {
        try {
          localStorage.setItem(storageKey, timeZone);
        } catch { /* quota / private mode */ }
      }
      return { storageKey, dateLocale, fallbackZones, isValidIanaTimeZone, read, write };
    }

    function bootEnterprise() {
      const root = document.getElementById("enterprise");
      root.hidden = false;
      let previous = "";
      const pulses = [];
      let selected = "";
      function showInspect(peer, data) {
        selected = peer.device_id || "";
        document.getElementById("entName").textContent = peer.name || peer.hostname || "Station";
        const state = peerPresenceState(peer);
        document.getElementById("entState").textContent = state;
        document.getElementById("entDecision").textContent = clipDashboardText((data.decisions || [])[0] || "");
        document.getElementById("entHandoff").textContent = clipDashboardText((data.handoffs || [])[0] || "");
      }
      function renderPulses() {
        const box = document.getElementById("entPulses");
        box.replaceChildren();
        pulses.slice(-4).forEach((text) => {
          const mark = document.createElement("span");
          mark.className = "pulse";
          mark.textContent = clipDashboardText(text);
          box.appendChild(mark);
        });
      }
      async function tick() {
        const res = await fetch("/api/peers", { cache: "no-store" });
        const data = await res.json();
        const circle = (data.circles || [])[0] || {};
        document.getElementById("entCircle").textContent = circle.name || "Circle";
        document.getElementById("entHost").textContent = data.hostname || "this station";
        document.getElementById("entClock").textContent = new Date().toLocaleTimeString();
        const members = circle.members || [];
        const rows = document.getElementById("entRows");
        const fit = Math.max(1, Math.floor(rows.clientHeight / 44));
        const shown = members.slice(0, fit);
        rows.replaceChildren();
        shown.forEach((peer) => {
          const row = document.createElement("button");
          row.type = "button";
          row.className = "ent-row" + (peer.endpoint ? "" : " offline");
          const name = document.createElement("span");
          name.textContent = peer.name || peer.hostname || "computer";
          const state = document.createElement("span");
          state.className = "state";
          state.textContent = peerPresenceState(peer);
          const detail = document.createElement("span");
          detail.textContent = clipDashboardText((data.handoffs || [])[0] || (data.events || [])[0] || "");
          row.append(name, state, detail);
          row.addEventListener("click", () => showInspect(peer, data));
          rows.appendChild(row);
        });
        if (members.length > shown.length) {
          const more = document.createElement("div");
          more.className = "ent-more";
          more.textContent = "+" + (members.length - shown.length);
          rows.appendChild(more);
        }
        const signature = JSON.stringify(data.events || []);
        if (previous && signature !== previous && (data.events || [])[0]) pulses.push(data.events[0]);
        if (pulses.length > 4) pulses.splice(0, pulses.length - 4);
        previous = signature;
        renderPulses();
        const current = members.find((peer) => peer.device_id === selected) || members.find((peer) => peer.self) || members[0];
        if (current) showInspect(current, data);
      }
      tick();
      setInterval(tick, 3000);
    }

    function catalogEntries(network) {
      const out = [];
      for (const computer of network.computers || []) {
        for (const folder of computer.tree || []) {
          for (const kind of folder.kinds || []) {
            for (const item of kind.items || []) {
              out.push({ computer, folder, kind: kind.kind, item });
            }
          }
        }
      }
      return out;
    }

    async function fetchMeshConversations(id) {
      if (!id) return [];
      const convRes = await fetch("/api/chat/conversations?mesh_id=" + encodeURIComponent(id), { cache: "no-store" });
      if (!convRes.ok) return [];
      const body = await convRes.json();
      return body.conversations || [];
    }

    async function submitJoinPayload(payload) {
      const res = await fetch("/api/circle/join", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const body = await res.json().catch(() => ({}));
      return { res, body };
    }

    async function bootOrbit(THREE) {

    const stage = document.getElementById("stage");
    const labels = document.getElementById("labels");
    const popover = document.getElementById("popover");
    const dock = document.getElementById("dock");
    const drawer = document.getElementById("drawer");
    const RADIUS = 1.18;
    let drawerKind = "";
    let selectedDevice = "";
    let openStation = null;
    let lastData = null;
    let lastPulse = "";
    let lastDockSig = "";
    let stations = [];
    let dragging = false;
    let moved = false;
    let lastX = 0;
    let lastY = 0;
    let world = null;
    let meshID = "";
    let selfID = "";
    let discovered = [];
    let discoveredOutsideCircle = [];
    const NEARBY_HIDDEN_KEY = "overdriveNearbyHidden";
    const nearbyForgotten = createDismissStore(NEARBY_HIDDEN_KEY);
    const circleHidden = createDismissStore(CIRCLE_HIDDEN_STORAGE_KEY);
    const nearbyPeerKey = (peer) => {
      const id = String(peer.device_id || "").trim();
      if (id) return discoveryStableKey(peer);
      const host = String(peer.host || "").trim();
      const port = peer.port != null && peer.port !== "" ? String(peer.port) : "";
      if (host && port) return "ep:" + host + ":" + port;
      return discoveryStableKey(peer);
    }
    function readNearbyHidden() {
      return nearbyForgotten.read();
    }
    function writeNearbyHidden(set) {
      try {
        localStorage.setItem(NEARBY_HIDDEN_KEY, JSON.stringify([...set]));
      } catch { /* quota / private mode */ }
    }
    function filterVisibleNearby(peers) {
      return filterForgottenNearby(dedupeDiscoveredPeers(peers), readNearbyHidden()).filter((peer) => nearbyPeerKey(peer));
    }
    function nearbyHiddenCount() {
      return readNearbyHidden().size;
    }
    function syncDiscoveredFromOutsideCircle() {
      discovered = filterVisibleNearby(discoveredOutsideCircle);
      const nearCount = document.getElementById("nearbyCount");
      if (nearCount) nearCount.textContent = discovered.length ? String(discovered.length) : "";
      renderDiscovered();
      if (!stack.hidden) renderStack(stackSearch.value);
    }
    function forgetNearbyPeer(peer) {
      nearbyForgotten.addAll(nearbyForgottenKeys(peer));
      nearbyPeerKey(peer) && nearbyForgotten.add(nearbyPeerKey(peer));
      const deviceID = String(peer.device_id || "").trim();
      if (deviceID) {
        fetch("/api/discovery/forget", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ device_id: deviceID })
        }).catch(() => {});
        discoveredOutsideCircle = discoveredOutsideCircle.filter(
          (item) => String(item.device_id || "").trim() !== deviceID
        );
      }
      syncDiscoveredFromOutsideCircle();
    }
    function forgetCircleMember(peer) {
      const deviceID = String(peer.device_id || "").trim();
      if (!deviceID || peer.self) return;
      circleHidden.add(deviceID);
      if (lastData) renderDock(lastData);
      if (!stack.hidden) renderStack(stackSearch.value);
    }
    globalThis.forgetCircleMember = forgetCircleMember;
    function clearNearbyHidden() {
      writeNearbyHidden(new Set());
      syncDiscoveredFromOutsideCircle();
    }
    const meshChat = createMeshChat({
      el,
      roster,
      computerLabel,
      peerAlive: alive,
      peerPresenceLabel(peer) {
        if (peer.self) return "this station";
        if (peer.revoked) return "revoked";
        return alive(peer) ? "online" : "offline";
      },
      getData() { return lastData; },
      async onSent() {
        const peersRes = await fetch("/api/peers", { cache: "no-store" });
        if (peersRes.ok) await refreshMesh(await peersRes.json());
      }
    });
    const meshCall = createMeshCall({
      openConversation(id) {
        openMessages();
        meshChat.openConversationById(id);
      },
      peerLabel(id) {
        const member = roster(lastData || []).find((p) => p.device_id === id);
        return member ? computerLabel(member, lastData) : id;
      }
    });
    function syncTerminalComposer(count) {
      const btn = document.getElementById("messageTerminal");
      if (!btn) return;
      const n = typeof count === "number" ? count : liveSessions.openTabCount();
      const live = n > 0;
      btn.dataset.mode = live ? "share" : "open";
      if (live) {
        btn.setAttribute("aria-label", "Share terminal");
        btn.title = "Share terminal";
      } else {
        btn.setAttribute("aria-label", "Open live terminal");
        btn.title = "Open live terminal, then Send to chat";
      }
    }
    const liveSessions = createLiveSessions({
      getData() { return lastData; },
      getProjectRoot() {
        return (lastData?.project_root) || ".";
      },
      selfDeviceId() { return selfID; },
      roster,
      computerLabel,
      chatTarget() { return meshChat.outboundTarget(); },
      onTabsChanged(count) { syncTerminalComposer(count); },
      async onShared() {
        const panel = document.getElementById("messages");
        if (panel?.hidden) setMessagesOpen(true);
        const peersRes = await fetch("/api/peers", { cache: "no-store" });
        if (peersRes.ok) await refreshMesh(await peersRes.json());
      }
    });

    function icon(id) {
      return document.getElementById(id).content.firstElementChild.cloneNode(true);
    }
    const circleApp = document.getElementById("circleApp");
    const stack = document.getElementById("stack");
    const stackSearch = document.getElementById("stackSearch");
    const find = document.getElementById("find");
    const findSearch = document.getElementById("findSearch");
    document.getElementById("ctxToggle").prepend(icon("iconContexts"));
    document.getElementById("actToggle").prepend(icon("iconActivity"));
    document.getElementById("legendApp").prepend(icon("iconLegend"));
    document.getElementById("findApp").prepend(icon("iconSearch"));
    document.getElementById("msgApp").prepend(icon("iconMessages"));
    document.getElementById("termApp")?.prepend(icon("iconTerminal"));
    document.getElementById("nearbyApp").prepend(icon("iconNearby"));
    document.getElementById("settingsApp").prepend(icon("iconSettings"));
    dock.addEventListener("click", (ev) => ev.stopPropagation());
    drawer.addEventListener("click", (ev) => ev.stopPropagation());
    stack.addEventListener("click", (ev) => ev.stopPropagation());
    find.addEventListener("click", (ev) => ev.stopPropagation());
    document.getElementById("ctxToggle").addEventListener("click", () => toggleDrawer("contexts"));
    document.getElementById("actToggle").addEventListener("click", () => toggleDrawer("activity"));
    document.getElementById("legendApp").addEventListener("click", () => toggleDrawer("legend"));
    document.getElementById("findApp").addEventListener("click", toggleFind);
    document.getElementById("msgApp").addEventListener("click", (ev) => {
      ev.stopPropagation();
      openMessages();
    });
    document.getElementById("termApp")?.addEventListener("click", openTerminal);
    document.getElementById("liveSessions")?.addEventListener("click", (ev) => ev.stopPropagation());
    document.getElementById("nearbyApp").addEventListener("click", openNearby);
    document.getElementById("settingsApp").addEventListener("click", openSettings);
    document.getElementById("msgClose").addEventListener("click", closeMessages);
    function startMeshCall(mode) {
      const plan = meshChat.callTargets();
      if (!plan.targets.length) {
        openMessages();
        return;
      }
      const convId = plan.conversationId || "";
      if (plan.targets.length > 1) {
        meshCall.startGroupCall(plan.targets, plan.label, mode, convId).catch(() => {});
        return;
      }
      meshCall.startCall(plan.targets[0], plan.label, mode, convId).catch(() => {});
    }
    document.getElementById("msgCall")?.addEventListener("click", () => startMeshCall("voice"));
    document.getElementById("msgVideo")?.addEventListener("click", () => startMeshCall("video"));
    document.getElementById("callPanel")?.addEventListener("click", (ev) => ev.stopPropagation());
    document.getElementById("nearbyClose").addEventListener("click", closeNearby);
    document.getElementById("settingsClose").addEventListener("click", closeSettings);
    document.getElementById("messages").addEventListener("click", (ev) => ev.stopPropagation());
    document.getElementById("nearby").addEventListener("click", (ev) => ev.stopPropagation());
    document.getElementById("settings").addEventListener("click", (ev) => ev.stopPropagation());
    document.getElementById("meshPause").addEventListener("click", () => postMesh("/api/mesh/pause"));
    document.getElementById("meshResume").addEventListener("click", () => postMesh("/api/mesh/resume"));
    document.getElementById("drawerClose").addEventListener("click", () => toggleDrawer(drawerKind));
    document.getElementById("stackClose").addEventListener("click", closeStack);
    document.getElementById("copyInvite").addEventListener("click", copyConnection);
    document.getElementById("joinDevice").addEventListener("click", joinConnection);
    document.getElementById("nearbyConnection").addEventListener("click", () => openManualConnection("joinJSON"));
    document.getElementById("chips").addEventListener("change", () => {
      selectedDevice = document.getElementById("chips").value;
      if (lastData) renderDock(lastData);
      loadContexts();
    });
    bindChipsPicker();
    circleApp.addEventListener("click", overview);
    stackSearch.addEventListener("input", () => renderStack(stackSearch.value));
    findSearch.addEventListener("input", () => renderFind(findSearch.value));
    document.getElementById("popContexts").addEventListener("click", () => {
      const peer = roster(lastData || {}).find((p) => p.device_id === openStation);
      if (!peer) return;
      selectedDevice = peer.device_id;
      if (drawerKind === "contexts") hidePopover();
      else toggleDrawer("contexts");
      renderDock(lastData);
      loadContexts();
    });
    const menubarPopoverPairs = [];
    const openMenubarPopover = (popover) => {
      if (!popover) return;
      popover.hidden = false;
      if (typeof popover.show === "function" && !popover.open) {
        try { popover.show(); } catch { /* already open */ }
      }
    }
    const hideMenubarPopover = (popover) => {
      if (!popover) return;
      if (typeof popover.close === "function" && popover.open) {
        try { popover.close(); } catch { /* */ }
      }
      popover.hidden = true;
    }
    function closeMenubarPopovers() {
      menubarPopoverPairs.forEach(({ trigger, popover, onClose }) => {
        onClose?.();
        hideMenubarPopover(popover);
        trigger?.setAttribute("aria-expanded", "false");
      });
    }
    document.addEventListener("click", (ev) => {
      const origin = ev.target instanceof Element ? ev.target : null;
      if (origin?.closest("#dock, #messages, #callPanel, #nearby, #settings, #liveSessions")) return;
      closeMenubarPopovers();
      hidePopover();
      closeStack();
      closeFind();
      closeApps();
    });
    function handleFindShortcut(ev, typing) {
      if (ev.key !== "/" || typing || ev.metaKey || ev.ctrlKey || ev.altKey) return false;
      ev.preventDefault();
      if (find.hidden) openFind();
      else findSearch.focus({ preventScroll: true });
      return true;
    }

    function handleEscapeShortcut() {
      const mediaModal = document.getElementById("chatMediaModal");
      if (mediaModal && !mediaModal.hidden) return;
      const messages = document.getElementById("messages");
      const live = document.getElementById("liveSessions");
      if (messages && !messages.hidden) closeMessages();
      else if (live && !live.hidden) liveSessions.hidePanel();
      else if (!document.getElementById("nearby").hidden) closeNearby();
      else if (!document.getElementById("settings").hidden) closeSettings();
      else if (!find.hidden) closeFind();
      else if (!stack.hidden) closeStack();
      else if (drawerKind) toggleDrawer(drawerKind);
      else if (document.querySelector(".menubar-popover:not([hidden])")) closeMenubarPopovers();
      else hidePopover();
    }

    function onGlobalKeydown(ev) {
      const typing = ev.target && (ev.target.tagName === "INPUT" || ev.target.tagName === "TEXTAREA");
      if (handleFindShortcut(ev, typing)) return;
      if (ev.key !== "Escape" || typing) return;
      handleEscapeShortcut();
    }
    document.addEventListener("keydown", onGlobalKeydown);
    const menubarTz = createMenubarTimeZoneStore({
      storageKey: "overdriveMenubarTimeZone",
      dateLocale: "en-US",
      fallbackZones: [
        "UTC",
        "America/Sao_Paulo",
        "America/New_York",
        "America/Los_Angeles",
        "Europe/London",
        "Europe/Paris",
        "Asia/Tokyo"
      ]
    });
    const MENUBAR_DATE_LOCALE = menubarTz.dateLocale;
    const MENUBAR_TZ_FALLBACK = menubarTz.fallbackZones;
    const isValidIanaTimeZone = menubarTz.isValidIanaTimeZone;
    const readMenubarTimeZone = menubarTz.read;
    const writeMenubarTimeZone = menubarTz.write;
    const utcOffsetLabel = (minutes) => {
      if (minutes === 0) return "UTC";
      const hours = minutes / 60;
      if (hours === Math.trunc(hours)) {
        return hours > 0 ? "UTC+" + hours : "UTC" + hours;
      }
      const sign = minutes > 0 ? "+" : "-";
      const abs = Math.abs(minutes);
      const h = Math.floor(abs / 60);
      const m = abs % 60;
      return "UTC" + sign + h + ":" + String(m).padStart(2, "0");
    }
    const tzOffsetMinutes = (timeZone, date = new Date()) => {
      try {
        const parts = new Intl.DateTimeFormat("en-US", {
          timeZone,
          year: "numeric",
          month: "2-digit",
          day: "2-digit",
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
          hour12: false
        }).formatToParts(date);
        const pick = (type) => Number(parts.find((p) => p.type === type)?.value);
        const asUtc = Date.UTC(pick("year"), pick("month") - 1, pick("day"), pick("hour"), pick("minute"), pick("second"));
        return Math.round((asUtc - date.getTime()) / 60000);
      } catch {
        return 0;
      }
    }
    const calendarYmdInTimeZone = (timeZone, date = new Date()) => {
      try {
        const parts = new Intl.DateTimeFormat("en-CA", {
          timeZone,
          year: "numeric",
          month: "2-digit",
          day: "2-digit"
        }).formatToParts(date);
        const pick = (type) => Number(parts.find((p) => p.type === type)?.value);
        return { year: pick("year"), month: pick("month") - 1, day: pick("day") };
      } catch {
        return { year: date.getFullYear(), month: date.getMonth(), day: date.getDate() };
      }
    }
    function menubarTimeZoneShortLabel(timeZone) {
      const offset = tzOffsetMinutes(timeZone);
      const city = timeZone.split("/").pop()?.replaceAll("_", " ") || timeZone;
      return utcOffsetLabel(offset) + " · " + city;
    }
    function formatMenubarDateTimeSummary(timeZone, date = new Date()) {
      const datePart = new Intl.DateTimeFormat(MENUBAR_DATE_LOCALE, {
        timeZone,
        weekday: "short",
        day: "numeric",
        month: "short"
      }).format(date);
      const timePart = new Intl.DateTimeFormat(MENUBAR_DATE_LOCALE, {
        timeZone,
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false
      }).format(date);
      return datePart + " " + timePart + " · " + menubarTimeZoneShortLabel(timeZone);
    }
    function formatMenubarClockLine(timeZone, date = new Date()) {
      return new Intl.DateTimeFormat(MENUBAR_DATE_LOCALE, {
        timeZone,
        weekday: "long",
        day: "numeric",
        month: "long",
        year: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false
      }).format(date);
    }
    function formatMenubarTzComboboxLabel(timeZone, refDate = new Date()) {
      const offset = tzOffsetMinutes(timeZone, refDate);
      const name = menubarTzReadableName(timeZone);
      return utcOffsetLabel(offset) + " · " + name;
    }
    function syncMenubarTzComboboxLabel() {
      const valueEl = document.getElementById("barDateTimeTzValue");
      if (!valueEl) return;
      valueEl.textContent = formatMenubarTzComboboxLabel(readMenubarTimeZone());
    }
    const menubarTzReadableName = (id) => {
      return id.replaceAll("_", " ");
    }
    let menubarTzCatalog = null;
    function menubarTzValues() {
      try {
        if (typeof Intl.supportedValuesOf === "function") return Intl.supportedValuesOf("timeZone");
      } catch { /* unsupported */ }
      return MENUBAR_TZ_FALLBACK.slice();
    }
    function getMenubarTzCatalog(refDate = new Date()) {
      menubarTzCatalog = menubarTzValues().map((id) => ({
        id,
        offset: tzOffsetMinutes(id, refDate),
        label: menubarTzReadableName(id)
      })).sort((a, b) => a.offset - b.offset || a.id.localeCompare(b.id));
      return menubarTzCatalog;
    }
    function renderMenubarCalendar(container, refDate, timeZone) {
      if (!container) return;
      const view = refDate instanceof Date ? refDate : new Date();
      const ymd = calendarYmdInTimeZone(timeZone, view);
      const year = ymd.year;
      const month = ymd.month;
      const today = calendarYmdInTimeZone(timeZone);
      const weekdayFmt = new Intl.DateTimeFormat(MENUBAR_DATE_LOCALE, { weekday: "narrow" });
      const startPad = new Date(year, month, 1).getDay();
      const daysInMonth = new Date(year, month + 1, 0).getDate();
      container.replaceChildren();
      const weekdays = document.createElement("div");
      weekdays.className = "menubar-cal__weekdays";
      weekdays.setAttribute("aria-hidden", "true");
      for (let i = 0; i < 7; i += 1) {
        const d = new Date(2024, 0, 7 + i);
        const cell = document.createElement("span");
        cell.textContent = weekdayFmt.format(d);
        weekdays.appendChild(cell);
      }
      const grid = document.createElement("div");
      grid.className = "menubar-cal__grid";
      grid.setAttribute("role", "grid");
      grid.setAttribute("aria-readonly", "true");
      for (let p = 0; p < startPad; p += 1) {
        const pad = document.createElement("span");
        pad.className = "menubar-cal__pad";
        pad.textContent = "0";
        grid.appendChild(pad);
      }
      for (let day = 1; day <= daysInMonth; day += 1) {
        const cell = document.createElement("span");
        cell.className = "menubar-cal__day";
        cell.textContent = String(day);
        cell.setAttribute("role", "gridcell");
        if (today.year === year && today.month === month && today.day === day) {
          cell.classList.add("is-today");
          cell.setAttribute("aria-current", "date");
        }
        grid.appendChild(cell);
      }
      container.append(weekdays, grid);
    }
    let menubarTzListActiveIndex = -1;
    const menubarTzListOptions = (list) => {
      if (!list) return [];
      return [...list.querySelectorAll('[role="option"]')];
    }
    function setMenubarTzListActive(list, index) {
      const options = menubarTzListOptions(list);
      options.forEach((el, i) => el.classList.toggle("is-active", i === index));
      menubarTzListActiveIndex = index;
      if (index >= 0 && options[index]) options[index].scrollIntoView({ block: "nearest" });
    }
    function selectMenubarTimeZone(timeZone, { list, search, closePanel } = {}) {
      if (!isValidIanaTimeZone(timeZone)) return;
      writeMenubarTimeZone(timeZone);
      menubarTzCatalog = null;
      syncMenubarTzComboboxLabel();
      tickMenubarDateTime();
      if (list) renderMenubarTzList(list, search?.value ?? "");
      closePanel?.();
    }
    function renderMenubarTzList(list, query = "") {
      if (!list) return;
      const current = readMenubarTimeZone();
      const q = query.trim().toLowerCase();
      const catalog = getMenubarTzCatalog();
      list.replaceChildren();
      const frag = document.createDocumentFragment();
      let shown = 0;
      for (const entry of catalog) {
        if (q && !entry.id.toLowerCase().includes(q) && !entry.label.toLowerCase().includes(q)) continue;
        shown += 1;
        const item = document.createElement("li");
        item.setAttribute("role", "option");
        item.dataset.timeZone = entry.id;
        item.setAttribute("aria-selected", entry.id === current ? "true" : "false");
        const off = document.createElement("span");
        off.className = "menubar-listbox__offset";
        off.textContent = utcOffsetLabel(entry.offset);
        const name = document.createElement("span");
        name.className = "menubar-listbox__name";
        name.textContent = entry.label;
        item.append(off, name);
        item.addEventListener("click", (ev) => {
          ev.stopPropagation();
          selectMenubarTimeZone(entry.id, {
            list,
            search: document.getElementById("barDateTimeTzSearch"),
            closePanel: () => closeMenubarTzCombobox()
          });
        });
        frag.appendChild(item);
      }
      if (shown === 0) {
        const empty = document.createElement("li");
        empty.className = "menubar-listbox__empty";
        empty.textContent = "No time zones found.";
        frag.appendChild(empty);
      }
      list.appendChild(frag);
      const options = menubarTzListOptions(list);
      const selectedIdx = options.findIndex((el) => el.getAttribute("aria-selected") === "true");
      let tzActiveIdx = selectedIdx;
      if (tzActiveIdx < 0) tzActiveIdx = options.length ? 0 : -1;
      setMenubarTzListActive(list, tzActiveIdx);
    }
    function closeMenubarTzCombobox() {
      const panel = document.getElementById("barDateTimeTzPanel");
      const trigger = document.getElementById("barDateTimeTzTrigger");
      const search = document.getElementById("barDateTimeTzSearch");
      if (!panel || panel.hidden) return;
      panel.hidden = true;
      trigger?.setAttribute("aria-expanded", "false");
      search?.setAttribute("aria-expanded", "false");
      if (search) search.value = "";
      const list = document.getElementById("barDateTimeTzList");
      renderMenubarTzList(list, "");
    }
    function openMenubarTzCombobox() {
      const panel = document.getElementById("barDateTimeTzPanel");
      const trigger = document.getElementById("barDateTimeTzTrigger");
      const search = document.getElementById("barDateTimeTzSearch");
      const list = document.getElementById("barDateTimeTzList");
      if (!panel || !trigger) return;
      panel.hidden = false;
      trigger.setAttribute("aria-expanded", "true");
      search?.setAttribute("aria-expanded", "true");
      renderMenubarTzList(list, search?.value ?? "");
      requestAnimationFrame(() => {
        search?.focus();
        list?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
      });
    }
    function tickMenubarDateTime() {
      const timeZone = readMenubarTimeZone();
      const now = new Date();
      const summary = document.getElementById("barDateTimeSummary");
      if (summary) {
        summary.textContent = formatMenubarDateTimeSummary(timeZone, now);
        summary.dateTime = now.toISOString();
      }
      const clockLine = document.getElementById("barDateTimeClock");
      if (clockLine) clockLine.textContent = formatMenubarClockLine(timeZone, now);
      syncMenubarTzComboboxLabel();
      const cal = document.getElementById("barDateTimeCal");
      const pop = document.getElementById("barDateTimePopover");
      if (cal && pop && !pop.hidden) renderMenubarCalendar(cal, now, timeZone);
    }
    function bindMenubarPopover(trigger, popover, { onOpen, onClose } = {}) {
      if (!trigger || !popover) return;
      menubarPopoverPairs.push({ trigger, popover, onClose });
      trigger.addEventListener("click", (ev) => {
        ev.stopPropagation();
        const opening = popover.hidden;
        closeMenubarPopovers();
        if (opening) {
          onOpen?.();
          openMenubarPopover(popover);
          trigger.setAttribute("aria-expanded", "true");
        }
      });
      popover.addEventListener("click", (ev) => ev.stopPropagation());
      trigger.addEventListener("keydown", (ev) => ev.stopPropagation());
    }
    function handleMenubarTzListKeydown(ev, { list, search }) {
      const options = menubarTzListOptions(list);
      if (!options.length) return;
      if (ev.key === "ArrowDown") {
        ev.preventDefault();
        const next = menubarTzListActiveIndex < 0 ? 0 : Math.min(menubarTzListActiveIndex + 1, options.length - 1);
        setMenubarTzListActive(list, next);
      } else if (ev.key === "ArrowUp") {
        ev.preventDefault();
        const next = menubarTzListActiveIndex < 0 ? options.length - 1 : Math.max(menubarTzListActiveIndex - 1, 0);
        setMenubarTzListActive(list, next);
      } else if (ev.key === "Enter") {
        const active = options[menubarTzListActiveIndex];
        if (active?.dataset.timeZone) {
          ev.preventDefault();
          selectMenubarTimeZone(active.dataset.timeZone, { list, search, closePanel: () => closeMenubarTzCombobox() });
        }
      } else if (ev.key === "Escape") {
        ev.preventDefault();
        ev.stopPropagation();
        closeMenubarTzCombobox();
      }
    }
    function initMenubarChrome() {
      const tzList = document.getElementById("barDateTimeTzList");
      const tzSearch = document.getElementById("barDateTimeTzSearch");
      const tzTrigger = document.getElementById("barDateTimeTzTrigger");
      const tzPanel = document.getElementById("barDateTimeTzPanel");
      syncMenubarTzComboboxLabel();
      if (tzTrigger && tzPanel) {
        tzTrigger.addEventListener("click", (ev) => {
          ev.stopPropagation();
          if (tzPanel.hidden) openMenubarTzCombobox();
          else closeMenubarTzCombobox();
        });
        tzTrigger.addEventListener("keydown", (ev) => {
          ev.stopPropagation();
          if (ev.key === "Enter" || ev.key === " " || ev.key === "ArrowDown") {
            ev.preventDefault();
            openMenubarTzCombobox();
          } else if (ev.key === "Escape" && !tzPanel.hidden) {
            ev.preventDefault();
            closeMenubarTzCombobox();
          }
        });
      }
      if (tzSearch) {
        tzSearch.addEventListener("click", (ev) => ev.stopPropagation());
        tzSearch.addEventListener("input", () => renderMenubarTzList(tzList, tzSearch.value));
        tzSearch.addEventListener("keydown", (ev) => {
          ev.stopPropagation();
          handleMenubarTzListKeydown(ev, { list: tzList, search: tzSearch });
        });
      }
      if (tzList) {
        tzList.addEventListener("keydown", (ev) => {
          ev.stopPropagation();
          handleMenubarTzListKeydown(ev, { list: tzList, search: tzSearch });
        });
      }
      bindMenubarPopover(document.getElementById("barDateTimeBtn"), document.getElementById("barDateTimePopover"), {
        onOpen: () => {
          menubarTzCatalog = null;
          const tz = readMenubarTimeZone();
          renderMenubarCalendar(document.getElementById("barDateTimeCal"), new Date(), tz);
          closeMenubarTzCombobox();
          syncMenubarTzComboboxLabel();
        },
        onClose: () => closeMenubarTzCombobox()
      });
      bindMenubarPopover(document.getElementById("barListen"), document.getElementById("barListenPopover"));

      const fsBtn = document.getElementById("barFullscreen");
      function syncFullscreenUi() {
        const active = Boolean(document.fullscreenElement);
        fsBtn.classList.toggle("is-fullscreen", active);
        fsBtn.setAttribute("aria-label", active ? "Exit fullscreen" : "Enter fullscreen");
        fsBtn.title = active ? "Exit fullscreen" : "Enter fullscreen";
      }
      document.addEventListener("fullscreenchange", syncFullscreenUi);
      syncFullscreenUi();
      fsBtn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
        else (document.documentElement.requestFullscreen || document.documentElement.webkitRequestFullscreen)?.call(document.documentElement).catch(() => {});
      });
      fsBtn.addEventListener("keydown", (ev) => ev.stopPropagation());
    }
    initMenubarChrome();
    tickMenubarDateTime();
    setInterval(tickMenubarDateTime, 1000);

    // Dock magnification: icons swell along a cosine bell around the pointer, like macOS.
    const MAGNIFY = 0.55;
    function dockApps() {
      return [...dock.querySelectorAll(".app, .device")];
    }
    function relaxDock() {
      dockApps().forEach((app) => app.style.removeProperty("--s"));
    }
    // Rest geometry of the dock (icon centers before any magnification), mirroring the CSS metrics.
    function restLayout() {
      const size = Number.parseFloat(getComputedStyle(dock).getPropertyValue("--size")) || 54;
      const gap = 12;
      const pad = 12;
      const centers = [];
      let x = pad;
      const walk = (nodes) => {
        nodes.forEach((node, i) => {
          if (i > 0) x += gap;
          if (node.classList.contains("app") || node.classList.contains("device")) {
            centers.push(x + size / 2);
            x += size;
          } else if (node.classList.contains("dock-sep")) {
            x += 5;
          } else if (node.classList.contains("dock-devices")) {
            walk([...node.children]);
          }
        });
      };
      walk([...dock.children]);
      return { size, centers, width: x + pad };
    }
    dock.addEventListener("pointermove", (ev) => {
      if (ev.pointerType === "touch") return;
      const apps = dockApps();
      const { size, centers, width } = restLayout();
      const rect = dock.getBoundingClientRect();
      const x = (ev.clientX - rect.left) * (width / Math.max(rect.width, 1));
      apps.forEach((app, i) => {
        const distance = Math.abs(x - centers[i]) / (size * 2.4);
        const bell = distance >= 1 ? 0 : 0.5 + 0.5 * Math.cos(Math.PI * distance);
        app.style.setProperty("--s", (1 + MAGNIFY * bell).toFixed(3));
      });
    });
    dock.addEventListener("pointerleave", relaxDock);

    function overview() {
      selectedDevice = "";
      hidePopover();
      closeFind();
      closeApps();
      if (drawerKind) toggleDrawer(drawerKind);
      const self = (lastData && roster(lastData).find((peer) => peer.self)) || null;
      if (self) spinTo(self);
      if (lastData) renderDock(lastData);
      loadContexts();
    }

    const DRAWER_TITLE = { contexts: "Contexts", activity: "Activity", legend: "Legend" };
    function topologyMapOpen() {
      const topology = document.getElementById("topology");
      return !!(topology && !topology.hidden);
    }
    function parkSheets() {
      const ctx = document.getElementById("ctxBody");
      const act = document.getElementById("actBody");
      const legend = document.getElementById("legendBody");
      const drawer = document.getElementById("drawer");
      const memory = document.getElementById("viewMemory");
      const activity = document.getElementById("viewActivity");
      if (!ctx || !act || !drawer || !memory || !activity) return;
      const overlay = topologyMapOpen() && drawer.classList.contains("open");
      if (overlay && drawerKind === "contexts") drawer.appendChild(ctx);
      else memory.appendChild(ctx);
      if (overlay && drawerKind === "activity") drawer.appendChild(act);
      else activity.appendChild(act);
      if (legend?.parentElement !== drawer) drawer.appendChild(legend);
      if (!overlay) {
        const section = document.body.dataset.section || "";
        ctx.hidden = section !== "memory";
        act.hidden = section !== "activity";
      }
    }
    function toggleDrawer(kind) {
      drawerKind = drawerKind === kind ? "" : kind;
      drawer.classList.toggle("open", drawerKind !== "");
      drawer.toggleAttribute("inert", drawerKind === "");
      if (drawerKind) {
        hidePopover();
        closeStack();
        closeFind();
        closeApps();
      }
      document.getElementById("ctxBody").hidden = drawerKind !== "contexts";
      document.getElementById("actBody").hidden = drawerKind !== "activity";
      document.getElementById("legendBody").hidden = drawerKind !== "legend";
      document.getElementById("drawerTitle").textContent = DRAWER_TITLE[drawerKind] || "Contexts";
      document.getElementById("ctxToggle").setAttribute("aria-pressed", String(drawerKind === "contexts"));
      document.getElementById("actToggle").setAttribute("aria-pressed", String(drawerKind === "activity"));
      document.getElementById("legendApp").setAttribute("aria-pressed", String(drawerKind === "legend"));
      updateDrawerMeta();
      parkSheets();
    }
    let contextTotals = { items: 0, folders: 0 };
    const plural = (n, word) => {
      return n + " " + word + (n === 1 ? "" : "s");
    }
    function updateDrawerMeta() {
      const box = document.getElementById("drawerMeta");
      if (drawerKind === "legend") {
        box.textContent = "How to read the map";
        return;
      }
      if (drawerKind === "activity") {
        const total = Number(document.getElementById("actCount").textContent) || 0;
        box.textContent = "Live from " + ((lastData?.hostname) || "this station") + (total ? " · " + plural(total, "item") : "");
        return;
      }
      const members = roster(lastData || {});
      const current = members.find((peer) => peer.device_id === selectedDevice);
      const scope = current ? computerLabel(current, lastData || {}) : "Whole network";
      const tally = contextTotals.items
        ? plural(contextTotals.items, "context") + " in " + plural(contextTotals.folders, "folder")
        : "Nothing stored yet";
      box.textContent = scope + " · " + tally;
    }
    const computerLabel = (peer, data) => {
      return peer.name || peer.hostname || (peer.self ? data.hostname : "") || "computer";
    }
    function peerState(peer) {
      if (peer.revoked) return "revoked";
      if (peer.self) return "this station";
      return alive(peer) ? "online" : "offline";
    }
    function stateClass(peer) {
      if (peer.revoked) return "revoked";
      if (peer.self) return "self";
      return alive(peer) ? "" : "off";
    }
    function ledClass(peer) {
      if (peer.revoked) return "bad";
      if (peer.self) return "on";
      return alive(peer) ? "live" : "";
    }
    const relativeTime = (iso) => {
      const t = Date.parse(iso || "");
      if (!Number.isFinite(t)) return "";
      const minutes = Math.round((Date.now() - t) / 60000);
      if (minutes < 1) return "just now";
      if (minutes < 60) return minutes + " min ago";
      const hours = Math.round(minutes / 60);
      if (hours < 48) return hours + " h ago";
      return Math.round(hours / 24) + " d ago";
    }
    function peerDetails(peer, data) {
      const parts = [peer.endpoint || "no endpoint"];
      if (peer.self && data.platform) parts.push(data.platform);
      if (peer.remote_addr) parts.push("seen " + peer.remote_addr);
      const when = relativeTime(peer.last_seen);
      if (when) parts.push(when);
      return parts;
    }
    function appIndicator(peer) {
      if (peer.revoked) return "bad";
      if (peer.self) return "on";
      return alive(peer) ? "live" : "dim";
    }
    // Each machine keeps a stable screen colour derived from its id, so it stays recognisable between refreshes.
    const hueFor = (id) => {
      let h = 0;
      for (const ch of String(id || "")) h = (h * 31 + (ch.codePointAt(0) ?? 0)) >>> 0;
      return h % 6;
    }
    // Neighbours in the dock never share a colour: a clash with any of the previous four bumps to the next free hue.
    const hues = new Map();
    function assignHues(members) {
      hues.clear();
      const recent = [];
      members.filter((peer) => !peer.self).forEach((peer) => {
        let hue = hueFor(peer.device_id);
        for (let step = 0; step < 6 && recent.includes(hue); step++) hue = (hue + 1) % 6;
        hues.set(peer.device_id, hue);
        recent.push(hue);
        if (recent.length > 4) recent.shift();
      });
    }
    function deviceClasses(peer) {
      if (peer.revoked) return "revoked";
      if (peer.self) return "self";
      const hue = hues.has(peer.device_id) ? hues.get(peer.device_id) : hueFor(peer.device_id);
      return "hue-" + hue + (alive(peer) ? "" : " off");
    }
    function macIcon() {
      return icon("iconMacBook");
    }
    function rankPeer(peer) {
      if (peer.self) return 0;
      if (peer.revoked) return 3;
      return alive(peer) ? 1 : 2;
    }
    const seenAt = (peer) => {
      const t = Date.parse(peer.last_seen || "");
      return Number.isFinite(t) ? t : 0;
    }
    // This station first, then online machines by freshness, then offline, then revoked.
    function orderedRoster(data) {
      return roster(data).slice().sort((a, b) => {
        const rank = rankPeer(a) - rankPeer(b);
        if (rank) return rank;
        const seen = seenAt(b) - seenAt(a);
        if (seen) return seen;
        return computerLabel(a, data).localeCompare(computerLabel(b, data));
      });
    }
    function maxDock() {
      const host = document.getElementById("topology");
      const viewport = (host?.clientWidth) || document.documentElement.clientWidth || globalThis.innerWidth;
      return viewport < 760 ? 3 : 5;
    }
    // The dock shows a handful of machines; the selected one is always among them, the rest live in the stack.
    function dockSlice(members) {
      const limit = maxDock();
      if (members.length <= limit) return { shown: members, hidden: [] };
      const shown = members.slice(0, limit);
      const picked = members.find((peer) => peer.device_id === selectedDevice);
      if (picked && !shown.includes(picked)) shown[shown.length - 1] = picked;
      return { shown, hidden: members.filter((peer) => !shown.includes(peer)) };
    }
    function fillTip(tip, title, detail) {
      tip.textContent = title;
      if (detail) {
        const small = document.createElement("small");
        small.textContent = detail;
        tip.appendChild(small);
      }
      return tip;
    }
    function tipFor(title, detail) {
      const tip = document.createElement("span");
      tip.className = "tip";
      return fillTip(tip, title, detail);
    }
    function deviceTile(peer, data) {
      const app = document.createElement("button");
      app.type = "button";
      app.className = ["device", deviceClasses(peer), appIndicator(peer)].filter(Boolean).join(" ");
      app.setAttribute("aria-label", computerLabel(peer, data) + ", " + peerState(peer));
      app.setAttribute("aria-pressed", String(selectedDevice === peer.device_id));
      app.append(macIcon(), tipFor(computerLabel(peer, data), peerState(peer) + " · " + peerDetails(peer, data)[0]));
      if (peer.revoked) app.appendChild(el("span", "count bad", "–"));
      app.addEventListener("click", () => selectDevice(peer));
      return app;
    }
    function stackTile(hidden) {
      const app = document.createElement("button");
      app.type = "button";
      app.id = "stackApp";
      app.className = "app stack";
      app.setAttribute("aria-label", "Machines");
      app.setAttribute("aria-pressed", String(!stack.hidden));
      const parts = [icon("iconStack")];
      if (hidden.length) parts.push(el("span", "count", "+" + hidden.length));
      const detail = hidden.length ? plural(hidden.length, "more") + " in the list" : "Choose a machine or add one";
      parts.push(tipFor("Machines", detail));
      app.append(...parts);
      app.addEventListener("click", toggleStack);
      return app;
    }
    function toggleStack() {
      if (stack.hidden) openStack();
      else closeStack();
    }
    function openManualConnection(focusID) {
      openStack(focusID || "joinJSON", true);
    }
    function openStack(focusID, revealManual) {
      hidePopover();
      closeFind();
      closeApps();
      if (drawerKind) toggleDrawer(drawerKind);
      stack.hidden = false;
      stackSearch.value = "";
      renderStack("");
      const tile = document.getElementById("stackApp");
      if (tile) tile.setAttribute("aria-pressed", "true");
      loadInviteRule();
      const manual = document.getElementById("connectionManual");
      const manualFocus = focusID === "joinJSON";
      if (manual) manual.open = !!(revealManual || manualFocus);
      const target = typeof focusID === "string" ? document.getElementById(focusID) : stackSearch;
      if (target) target.focus({ preventScroll: true });
    }
    function closeStack() {
      if (stack.hidden) return;
      stack.hidden = true;
      const tile = document.getElementById("stackApp");
      if (tile) tile.setAttribute("aria-pressed", "false");
    }
    // Subjects already summarized on the circle. The catalog is the contexts API: subject and summary only.
    let catalog = null;
    let catalogToken = 0;
    function toggleFind() {
      if (find.hidden) openFind();
      else closeFind();
    }
    function openFind() {
      hidePopover();
      closeStack();
      closeApps();
      if (drawerKind) toggleDrawer(drawerKind);
      find.hidden = false;
      document.getElementById("findApp").setAttribute("aria-pressed", "true");
      findSearch.value = "";
      renderFind("");
      const token = ++catalogToken;
      loadCatalog().then(() => {
        if (token === catalogToken && !find.hidden) renderFind(findSearch.value);
      });
      findSearch.focus({ preventScroll: true });
    }
    function closeFind() {
      if (find.hidden) return;
      find.hidden = true;
      catalogToken += 1;
      document.getElementById("findApp").setAttribute("aria-pressed", "false");
    }
    async function loadCatalog() {
      const res = await fetch("/api/contexts", { cache: "no-store" });
      catalog = await res.json();
    }
    function entryText(entry) {
      return [entry.item.subject, entry.item.summary, kindLabel(entry.kind), entry.folder.name, entry.folder.path, entry.computer.name]
        .join(" ")
        .toLowerCase();
    }
    const entryTime = (entry) => {
      const t = Date.parse(entry.item.updated_at || "");
      return Number.isFinite(t) ? t : 0;
    }
    function renderFind(filter) {
      const box = document.getElementById("findResults");
      box.replaceChildren();
      if (!catalog) {
        box.appendChild(blank("Searching", "Looking through subjects already summarized on this circle."));
        return;
      }
      const needle = String(filter || "").trim().toLowerCase();
      let entries = catalogEntries(catalog).sort((a, b) => entryTime(b) - entryTime(a));
      if (needle) entries = entries.filter((entry) => entryText(entry).includes(needle));
      const shown = entries.slice(0, needle ? 40 : 8);
      const group = el("div", "group");
      let title = "Recent";
      if (needle) title = entries.length === 1 ? "1 result" : entries.length + " results";
      group.appendChild(el("div", "group-title", title));
      const card = el("div", "card");
      if (!shown.length) {
        card.appendChild(blank(
          needle ? "No Matches" : "Nothing summarized yet",
          needle ? "No subject or summary contains “" + filter.trim() + "”." : "Decisions, lessons and handoffs from this circle will show up here."
        ));
      }
      shown.forEach((entry) => card.appendChild(subjectHit(entry)));
      if (needle && entries.length > shown.length) {
        card.appendChild(listRow({ title: "Showing " + shown.length + " of " + entries.length, quiet: true }));
      }
      group.appendChild(card);
      box.appendChild(group);
    }
    function subjectHit(entry) {
      const hit = el("button", "hit");
      hit.type = "button";
      const where = [kindLabel(entry.kind), entry.folder.name].filter(Boolean).join(" · ");
      hit.appendChild(listRow({
        lead: dot(entry.kind),
        title: entry.item.subject,
        detail: entry.item.summary || where,
        wrap: true,
        aside: [entry.computer.name || ""]
      }));
      hit.addEventListener("click", () => openSubject(entry));
      return hit;
    }
    function openSubject(entry) {
      closeFind();
      selectedDevice = entry.computer.device_id || "";
      const peer = roster(lastData || {}).find((item) => item.device_id === selectedDevice);
      if (peer) spinTo(peer);
      if (lastData) renderDock(lastData);
      if (drawerKind !== "contexts") toggleDrawer("contexts");
      loadContexts();
    }
    function checkMark() {
      const mark = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      mark.setAttribute("viewBox", "0 0 16 16");
      mark.setAttribute("class", "check");
      mark.setAttribute("aria-hidden", "true");
      const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
      path.setAttribute("d", "M3.5 8.5l3 3 6-6.5");
      path.setAttribute("fill", "none");
      path.setAttribute("stroke", "currentColor");
      path.setAttribute("stroke-width", "1.8");
      path.setAttribute("stroke-linecap", "round");
      path.setAttribute("stroke-linejoin", "round");
      mark.appendChild(path);
      return mark;
    }
    function rowForgetButton(ariaLabel, onForget) {
      const btn = el("button", "row-forget close");
      btn.type = "button";
      btn.setAttribute("aria-label", ariaLabel);
      btn.addEventListener("click", (ev) => {
        ev.preventDefault();
        ev.stopPropagation();
        onForget();
      });
      return btn;
    }
    function machineRowShell(primary, forgetBtn) {
      if (!forgetBtn) return primary;
      const shell = el("div", "machine-row-shell");
      shell.append(primary, forgetBtn);
      return shell;
    }
    function stackCell(peer, data) {
      const cell = el("button", "machine-row");
      cell.type = "button";
      const selected = selectedDevice === peer.device_id;
      cell.setAttribute("aria-pressed", String(selected));
      const tile = el("span", "device " + deviceClasses(peer));
      tile.appendChild(macIcon());
      const text = el("span", "machine-copy");
      text.appendChild(el("b", "", computerLabel(peer, data)));
      const meta = el("small", "machine-presence");
      const on = alive(peer);
      const dot = el("span", "presence-dot " + (on ? "on" : "off"), "");
      dot.setAttribute("aria-hidden", "true");
      let stateKey = "offline";
      if (peer.revoked) stateKey = "revoked";
      else if (peer.self) stateKey = "self";
      else if (on) stateKey = "online";
      const label = el("span", "presence-label " + stateKey, peerState(peer));
      meta.append(dot, label);
      if (!on && !peer.self && !peer.revoked) {
        const when = relativeTime(peer.last_seen);
        if (when) meta.append(document.createTextNode(" · " + when));
      }
      text.appendChild(meta);
      cell.append(tile, text, selected ? checkMark() : el("span", "check-slot"));
      cell.addEventListener("click", () => {
        closeStack();
        if (selectedDevice === peer.device_id) spinTo(peer);
        else selectDevice(peer);
      });
      return cell;
    }
    function matchesPeer(peer, data, needle) {
      if (!needle) return true;
      return [computerLabel(peer, data), peer.endpoint, peer.device_id, peer.remote_addr]
        .some((value) => String(value || "").toLowerCase().includes(needle));
    }
    function renderStack(filter) {
      const grid = document.getElementById("stackGrid");
      const data = lastData || {};
      const needle = String(filter || "").trim().toLowerCase();
      const members = filterHiddenCircleMembers(orderedRoster(data), circleHidden.read())
        .filter((peer) => matchesPeer(peer, data, needle));
      const nearby = discovered.filter((peer) => discoveryMatch(peer, needle));
      grid.replaceChildren();
      if (!members.length && !nearby.length) {
        grid.appendChild(blank("No Matches", needle ? "No machine is called “" + filter.trim() + "”." : "No machines in this circle yet. Stations on your LAN show up under Nearby when Overdrive finds them."));
        return;
      }
      if (members.length) {
        const group = el("div", "group");
        group.appendChild(el("div", "group-title", "In this circle"));
        const card = el("div", "card");
        members.forEach((peer) => card.appendChild(stackCell(peer, data)));
        group.appendChild(card);
        grid.appendChild(group);
      }
      if (!nearby.length) return;
      const group = el("div", "group");
      group.appendChild(el("div", "group-title", "Nearby"));
      group.appendChild(el("p", "hint", "Seen on the local network. Not in the circle."));
      const card = el("div", "card");
      nearby.forEach((peer) => card.appendChild(discoveredCell(peer)));
      group.appendChild(card);
      grid.appendChild(group);
    }
    const discoveryMatch = (peer, needle) => {
      if (!needle) return true;
      return [peer.display_name, peer.friendly_name, peer.device_id, peer.os]
        .some((value) => String(value || "").toLowerCase().includes(needle));
    }
    const discoveryName = (peer) => {
      return peer.display_name || peer.friendly_name || peer.device_id || "Device";
    }
    function discoveredCell(peer) {
      const cell = el("div", "machine-row static");
      const tile = el("span", "device discovered");
      tile.appendChild(macIcon());
      const text = el("span", "machine-copy");
      text.append(el("b", "", discoveryName(peer)), el("small", "", "Not in the circle"));
      cell.append(tile, text, el("span", "check-slot"));
      const name = discoveryName(peer);
      return machineRowShell(cell, rowForgetButton("Forget " + name, () => forgetNearbyPeer(peer)));
    }
    function fitDock(count) {
      const viewport = document.documentElement.clientWidth || globalThis.innerWidth;
      const room = Math.max(240, viewport - 48);
      const slots = count + 8;
      const size = Math.min(54, Math.max(36, Math.floor((room - 16 * slots) / slots)));
      dock.style.setProperty("--size", size + "px");
    }
    function renderMenubarListen(data) {
      const field = document.getElementById("barListenField");
      const btn = document.getElementById("barListen");
      const addrEl = document.getElementById("barListenPopAddr");
      const uiEl = document.getElementById("barListenPopUi");
      const listen = data?.listen ? String(data.listen).trim() : "";
      if (!field || !btn) return;
      if (!listen) {
        field.hidden = true;
        delete btn.dataset.listen;
        btn.setAttribute("aria-label", "Not listening");
        btn.title = "Not listening";
        if (addrEl) addrEl.textContent = "";
        if (uiEl) {
          uiEl.hidden = true;
          uiEl.textContent = "";
        }
        return;
      }
      field.hidden = false;
      const summary = "Listening on " + listen;
      btn.dataset.listen = listen;
      btn.setAttribute("aria-label", summary);
      btn.title = summary;
      if (addrEl) addrEl.textContent = listen;
      const ui = data.ui ? String(data.ui).trim() : "";
      if (uiEl) {
        if (ui) {
          uiEl.hidden = false;
          uiEl.textContent = "Dashboard UI at " + ui;
        } else {
          uiEl.hidden = true;
          uiEl.textContent = "";
        }
      }
    }
    function renderMenubar(data, members, circle) {
      const links = members.filter((peer) => !peer.self && !peer.revoked && peer.endpoint).length;
      const text = (id, value) => { document.getElementById(id).textContent = value; };
      text("barCircle", circle ? (circle.name || "LAN") : "No circle");
      text("barMachines", plural(members.length, "machine"));
      text("barLinks", plural(links, "link"));
      renderMenubarListen(data);
      text("barPulse", humanEvent((data.events || [])[0]) || (data.hostname ? "station " + data.hostname : "circle atlas"));
      const anyLive = members.some((peer) => !peer.self && alive(peer));
      document.querySelector(".menubar .pulse .led").className = "led " + (anyLive ? "live" : "on");
    }
    let toastTimer = 0;
    function showToast(text) {
      const toast = document.getElementById("toast");
      document.getElementById("dockPulse").textContent = text;
      toast.classList.add("show");
      clearTimeout(toastTimer);
      toastTimer = setTimeout(() => toast.classList.remove("show"), 6000);
    }
    function renderDock(data) {
      const members = filterHiddenCircleMembers(orderedRoster(data), circleHidden.read());
      assignHues(members);
      const circle = (data.circles || [])[0];
      const box = document.getElementById("devices");
      const { shown, hidden } = dockSlice(members);
      const signature = JSON.stringify([maxDock(), hidden.length, shown.map((peer) => [
        peer.device_id, computerLabel(peer, data), peer.endpoint, appIndicator(peer), selectedDevice === peer.device_id
      ])]);
      if (signature !== lastDockSig) {
        const tiles = shown.map((peer) => deviceTile(peer, data));
        tiles.push(stackTile(hidden));
        box.replaceChildren(...tiles);
        lastDockSig = signature;
      }
      fitDock(shown.length + 1);
      if (!stack.hidden) renderStack(stackSearch.value);
      const anyLive = members.some((peer) => !peer.self && alive(peer));
      const circleTitle = circle ? (circle.name || "LAN") : "Offline";
      let circlePresence = "";
      if (anyLive) circlePresence = "live";
      else if (circle) circlePresence = "on";
      circleApp.className = ["app", "circle", circle ? "" : "offline", circlePresence].filter(Boolean).join(" ");
      fillTip(document.getElementById("circleName"), circleTitle, circle ? "listening " + (data.listen || "-") : "no circle paired");
      renderMenubar(data, members, circle);
      const latest = humanEvent((data.events || [])[0]);
      if (latest !== lastPulse) {
        if (latest) showToast(latest);
        lastPulse = latest;
      }
    }
    function selectDevice(peer) {
      if (selectedDevice === peer.device_id) {
        selectedDevice = "";
        hidePopover();
      } else {
        selectedDevice = peer.device_id;
        spinTo(peer);
        if (drawerKind) hidePopover();
        else openFor(peer);
      }
      if (lastData) renderDock(lastData);
      loadContexts();
    }
    function spinTo(peer) {
      const station = stations.find((s) => s.peer.device_id === peer.device_id);
      if (!station || !world) return;
      const p = station.group.position;
      world.spinTarget = -Math.atan2(p.x, p.z);
    }
    const tidyLine = (text) => {
      return String(text || "").replace(/^-\s*/, "");
    }
    // Peer sync lines arrive as "<device or endpoint>: online, sync ok, N pacotes" / "sem resposta (timeout)".
    // Show them the way the legend speaks: machine name, English, no ids.
    function machineName(key) {
      const peer = roster(lastData || {}).find((p) => p.device_id === key || (p.endpoint && p.endpoint === key));
      return peer ? computerLabel(peer, lastData || {}) : key;
    }
    function humanEvent(text) {
      const line = tidyLine(text);
      const ok = /^(\S+): online, sync ok, (\d+) pacotes$/.exec(line);
      if (ok) return machineName(ok[1]) + " synced · " + plural(Number(ok[2]), "packet");
      const timeout = /^(\S+): sem resposta \(timeout\)$/.exec(line);
      if (timeout) return machineName(timeout[1]) + " timed out";
      return line;
    }
    const KIND_LABEL = {
      decision: "Decisions", handoff: "Handoffs", lesson: "Lessons", rule: "Rules",
      procedure: "Procedures", preference: "Preferences", anti_pattern: "Anti-patterns",
      fact: "Facts", episode: "Episodes", event: "Events"
    };
    function kindLabel(kind) {
      if (KIND_LABEL[kind]) return KIND_LABEL[kind];
      const text = String(kind || "item").replaceAll("_", " ");
      return text.charAt(0).toUpperCase() + text.slice(1);
    }
    function el(tag, className, text) {
      const node = document.createElement(tag);
      if (className) node.className = className;
      if (text !== undefined) node.textContent = text;
      return node;
    }
    function dot(kind) {
      return el("i", "dot " + (kind || ""));
    }
    // One list row: optional leading symbol, title, secondary line, trailing text or chevron.
    function listRow(spec) {
      const row = el("div", "row" + (spec.lead ? "" : " plain") + (spec.quiet ? " quiet" : "") + (spec.kv ? " kv" : ""));
      if (spec.lead) {
        const lead = el("div", "row-lead");
        lead.appendChild(spec.lead);
        row.appendChild(lead);
      }
      const body = el("div", "row-body");
      const text = el("div", "row-text");
      let titleClass = "";
      if (spec.clamp) titleClass = "clamp";
      else if (spec.wrap) titleClass = "wrap";
      text.appendChild(el("b", titleClass, spec.title));
      if (spec.detail) text.appendChild(el("small", spec.wrap ? "wrap" : "", spec.detail));
      body.appendChild(text);
      if (spec.aside?.length) {
        const aside = el("div", "row-aside");
        spec.aside.forEach((piece) => aside.appendChild(typeof piece === "string" ? document.createTextNode(piece) : piece));
        body.appendChild(aside);
      }
      row.appendChild(body);
      return row;
    }
    function blank(title, text) {
      const box = el("div", "blank");
      box.append(icon("iconTray"), el("b", "", title), el("p", "", text));
      return box;
    }
    function renderFeed(data) {
      const feed = document.getElementById("feed");
      feed.replaceChildren();
      const groups = [
        ["Events", (data.events || []).map(humanEvent), "event"],
        ["Decisions", data.decisions || [], "decision"],
        ["Handoffs", data.handoffs || [], "handoff"]
      ];
      let total = 0;
      groups.forEach(([title, items, cls]) => {
        total += items.length;
        const group = el("div", "group");
        group.appendChild(el("div", "group-title", title));
        const card = el("div", "card");
        if (!items.length) card.appendChild(listRow({ title: "Nothing yet", quiet: true }));
        items.forEach((text) => card.appendChild(listRow({ lead: dot(cls), title: text, wrap: true })));
        group.appendChild(card);
        feed.appendChild(group);
      });
      document.getElementById("actCount").textContent = total ? String(total) : "";
      if (drawerKind === "activity") updateDrawerMeta();
    }
    const folderName = (path) => {
      const parts = (path || "").replaceAll("\\", "/").split("/").filter(Boolean);
      return parts.at(-1) || path;
    }
    const stationFolders = (peer, circle) => {
      if (peer.folders?.length) return peer.folders;
      return (circle?.folders) || [];
    }
    function alive(peer) {
      if (peer.revoked || !peer.endpoint) return false;
      if (!peer.last_seen) return true;
      const t = Date.parse(peer.last_seen);
      return Number.isFinite(t) && (Date.now() - t) < 15 * 60 * 1000;
    }
    function hidePopover() {
      openStation = null;
      popover.hidden = true;
    }
    function latLon(lat, lon, radius) {
      const phi = THREE.MathUtils.degToRad(90 - lat);
      const theta = THREE.MathUtils.degToRad(lon);
      return new THREE.Vector3(
        radius * Math.sin(phi) * Math.sin(theta),
        radius * Math.cos(phi),
        radius * Math.sin(phi) * Math.cos(theta)
      );
    }
    const placeMembers = (members) => {
      const self = members.find((m) => m.self) || members[0];
      const others = members.filter((m) => m !== self);
      const out = [];
      if (self) out.push({ peer: self, lat: -14, lon: 22 });
      const count = others.length;
      others.forEach((peer, i) => {
        let lat;
        let lon;
        if (count <= 4) {
          lon = count === 1 ? -42 : -78 + (i * 150) / (count - 1);
          lat = 10 + (i % 2) * 16 - (i % 3) * 5;
        } else {
          // Golden-angle lattice: evenly spread across the visible hemisphere, no two machines stacked.
          lon = -80 + 160 * ((i * 0.618034) % 1);
          lat = -30 + 66 * ((i + 0.5) / count);
          if (Math.abs(lat + 14) < 9 && Math.abs(lon - 22) < 16) lat += 12;
        }
        out.push({ peer, lat, lon });
      });
      return out;
    }
    // 1° land mask (360x180 bits, row 0 = lat 90..89, col 0 = lon -180..-179) derived from Natural Earth 110m.
    const LAND_MASK = [
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADg/wEAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADA/v8/AOD//z8AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD+///D",
      "/////5//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPAH/j/+//////8PAAAAgB0AAHgAAAAAAD8AAAAAAAAAAAAA",
      "AAAAAAAAAAAAAOD//wP8//////8BAACAfx4AAAAAAAAAAP4AAAAAAAAAAAAAAAAAAAAAAAA4+I73//n///////8AAAAAfwAAAAAA",
      "AAAAAAAfAAAAAAAAAAAAAAAAAAAAAOAQACDwP4D///////8BAAAAPAQAAAAAAAAAAAAYAAAAAAAAAAAAAAAAAAAAADzAgPP5H8D/",
      "/////38AAAAAAAAAAAAA4AEAAAD+DwAAAAAAAAAAAAAAAAAAAIC/g7PfBwAA/v////8AAAAAAAAAAADgBwAAwP//PwAA4B8AAAAA",
      "AAAAAAAAAAAMAAD8AgAA+P////8AAAAAAAAAAABwAAAA/P//AwAAAAAAAAAAAAAAAAAAAP8Ahvc5OwAA8P///38AAAAAAAAAAAAc",
      "AADg////34cHAAcAAAAAAAAAAAAAgN9/xzf8RwAA4P///z8AAAAAAAAAAAAPAB7g//////8fgB8AAAAAAT4AAAAAAO//B3f8/wsA",
      "4P///z8AAAAAAAAAAAAPAG////////8f2f8HAAAAAAD4HwAAAAD8H/D4/38A4P///xMAAAAAAPAHAAAAgN//////////////DwAA",
      "AAD+///h/4//H+/Bg/8BwP7//w8AAAAAwP8fAAAAj9///////////////88//v//////////////////////////////DwAA/P8c",
      "gEEAAAAAAAAAAAAAAAAA8P//////////////////////////////AwAA4AwAAGAAAAAAAAAAAAAAAAAAIP7/////////////////",
      "////////////AQBg4AAAABAAAAAAAAAAAAAAAAAAAf//////////////////////////////AByADwAAAAAAAAAAAAAAAAAAAAAA",
      "QID4///////////Pw/0DAP8HAMAfAACA//F//v////////////////////8/AAP4///////////BBPAHAP4HAAAAAADg//z/////",
      "//////////////////9/AID///////////8AQ8ABAPwBAAAAAAD8P/7///////////////////////9fAMD//////////z8AwA8A",
      "APgBAAAAAAD+H/7/////////////////////Of8BAID///z//////z8AwD8AAMABAAAAAAD+P/z///////////////////9/wH8A",
      "AAD8MwD//////x8AwH8MAAAAAAAAAAD+P2D///////////////////95cAAAAACAAwD8/////38AwP8eAAAAAAAAgAGcH/D/////",
      "/////////////wEAOAAAAADADgDA/////38AgP8/AAAAAAAAwANAH/T//////////////////wAAfgAAAAAwAAAA//////8PgP8/",
      "AAAAAAAAwAFwD/7/////////////////PwAAfwAAAAAGAAAA//////8/wP//AAAAAAAAwAOwA/7/////////////////DwCAPwAA",
      "AIAAAAAA/v//////8///BwAAAAAAOAcgcP//////////////////HwAAHwAAAAAAAACA+P//////4///DwAAAAAAOA74////////",
      "/////////////wUADwAAAAAAAAAA8P//////4///DwAAAAAAGD///////////////////////wUAAwAAAAAAAAAA8P//////7///",
      "FwAAAAAAAB///////////////////////wUAAgAAAAAAAAAA0P//////////CAAAAAAAgOH//////////////////////w0AAAAA",
      "AAAAAAAAYP////////8xLAAAAAAAAPz//////////////////////wwAAAAAAAAAAAAAAP7//////38HfgAAAAAAgP//////////",
      "/////////////wQAAAAAAAAAAAAAAP3///////8HUAAAAAAAAP7/////////////////////fwQAAAAAAAAAAAAAAP////////+X",
      "AAAAAAAAAPz///93/D/+////////////PwwAAAAAAAAAAAAAAP////////9/AAAAAAAAAPj//f9j/g/+////////////HwAAAAAA",
      "AAAAAAAAAP////////8MAAAAAAAAAPj/+P8B/Mf/////////////DwQAAAAAAAAAAAAAAP///////z8AAAAAAAAAcPzH8/8A8I//",
      "////////////Bx4AAAAAAAAAAAAAAP///////x8AAAAAAAAA+H+Aw/8AwA/+//////////9/AA8AAAAAAAAAAAAAAP///////x8A",
      "AAAAAAAA+D8Aj//w4R/8//////////8/AAAAAAAAAAAAAAAAAP///////wMAAAAAAAAA+B8wuE/+/z/+/////////98fAAMAAAAA",
      "AAAAAAAAAP///////wMAAAAAAAAA+A8wEMf//x/+/////////0cOAAMAAAAAAAAAAAAAAP7//////wEAAAAAAAAA+A8AAI7//x/8",
      "/////////wMOAAEAAAAAAAAAAAAAAP7//////wAAAAAAAAAA+AcABob//z/8/////////zccwAEAAAAAAAAAAAAAAPz//////wAA",
      "AAAAAAAAQOB/AAQ2/////////////x8c8AEAAAAAAAAAAAAAAPj//////wAAAAAAAAAAQPx/AAAA/////////////w8Y/gEAAAAA",
      "AAAAAAAAAPD/////fwAAAAAAAAAA4P8/AAAA/////////////w+EGwAAAAAAAAAAAAAAAMD/////HwAAAAAAAAAA8P9/AACA////",
      "/////////x/AAwAAAAAAAAAAAAAAAID/////DwAAAAAAAAAA+P//BwaA/////////////x/AAAAAAAAAAAAAAAAAAID9////BwAA",
      "AAAAAAAA/P//D3+E/////////////z9AAAAAAAAAAAAAAAAAAAD5////BwAAAAAAAAAA/P//f////////////////x8AAAAAAAAA",
      "AAAAAAAAAADy/x8GBgAAAAAAAAAA/P/////v/8///////////z8AAAAAAAAAAAAAAAAAAAD0/w8ABgAAAAAAAAAA/v////+//4//",
      "/////////z8AAAAAAAAAAAAAAAAAAADu/wcADgAAAAAAAACA//////8f/x/+/////////x8AAAAAAAAAAAAAAAAAAACI/wcALAAA",
      "AAAAAADA//////8//z/g/////////w8AAAAAAAAAAAAAAAAAAACQ/wcACAAAAAAAAADg//////9//r+A/////////wcAAAAAAAAA",
      "AAAAAAAAAAAQ/wMAAAAAAAAAAADg//////9//n8cgP///////ycAAAAAAAAAAAAAAAAAAAAA/gMAAAAAAAAAAADw////////+P9/",
      "AP///////xEAAAAAAAAAAAAAAAAAAAAA/AMAHQAAAAAAAADw////////+P//APz/f///fxAAAAAAAAAAAAAAAAAAAAAA+AcAYAAA",
      "AAAAAAD4////////+f9/APz/B///BgAAAAAAAAAAAAAAAAAAAAAA/AccgAEAAAAAAADw////////8f9/AOD/B/5/AAAAAAAAAAAA",
      "AAAAAQAAAAAA+A8eADgAAAAAAADw////////4f8/AOD/Afw/BgAAAAAAAAAAAAAAAAAAAAAA8J8PAPwCAAAAAADw////////4/8f",
      "AOD/APw/AgAAAAAAAAAAAAAAAAAAAAAAgP8PAAAAAAAAAADw////////x/8HAOB/APx/ADAAAAAAAAAAAAAAAAAAAAAAAP4PAAAA",
      "AAAAAAD4////////h/8BAOA/APz/ADAAAAAAAAAAAAAAAAAAAAAAAID/AAAAAAAAAAD4////////j/8AAMAPAMD/ATAAAAAAAAAA",
      "AAAAAAAAAAAAAAD/AQAAAAAAAAD4////////nx8AAMAPAMD/ATAAAAAAAAAAAAAAAAAAAAAAAAD8AAAAAAAAAAD4////////vwcA",
      "AIAPAMD/AcAAAAAAAAAAAAAAAAAAAAAAAADgAQAAAAAAAAD4////////fwAAAIAPAMD8AQABAAAAAAAAAAAAAAAAAAAAAADAAAgA",
      "AAAAAADw////////f2AAAAAPAID4AUACAAAAAAAAAAAAAAAAAAAAAADAAe9zAAAAAADg/////////34AAAAPAEBwAAgAAAAAAAAA",
      "AAAAAAAAAAAAAAAAE+9/AAAAAADA/////////38AAAAXAEAgAAQCAAAAAAAAAAAAAAAAAAAAAAAAz///AAAAAACA/////////z8A",
      "AAASAMAAAIACAAAAAAAAAAAAAAAAAAAAAAAAyP//AQAAAACA/////////z8AAAAwAIAAAEAHAAAAAAAAAAAAAAAAAAAAAAAAgP//",
      "AwAAAAAA/v///////x8AAAAwAAADAAMBAAAAAAAAAAAAAAAAAAAAAAAAwP//fwAAAAAA/A/+/////x8AAAAAAAAHAAcAAAAAAAAA",
      "AAAAAAAAAAAAAAAAgP///wAAAAAAEAD8/////w8AAAAAADAGwAcAAAAAAAAAAAAAAAAAAAAAAAAAgP///wEAAAAAAADA/////wcA",
      "AAAAAGAG4AEAAAAAAAAAAAAAAAAAAAAAAAAA4P///wEAAAAAAADA/////wMAAAAAAMAM+AMAAAAAAAAAAAAAAAAAAAAAAAAA4P//",
      "/wMAAAAAAADg/////wEAAAAAAIAL/gMQAAAAAAAAAAAAAAAAAAAAAAAA8P///wMAAAAAAADg////fwAAAAAAAIAH/vMQAAAAAAAA",
      "AAAAAAAAAAAAAAAA8P///wcAAAAAAADg////PwAAAAAAAAAP/gMAAQAAAAAAAAAAAAAAAAAAAAAA+P///38AAAAAAADg////PwAA",
      "AAAAAAAO/DmAAwAAAAAAAAAAAAAAAAAAAAAA+P///38BAAAAAADA////HwAAAAAAAAA+/DkA8gAAAAAAAAAAAAAAAAAAAAAA8P//",
      "//8fAAAAAACA////DwAAAAAAAAA8wChE/gcAAAAAAAAAAAAAAAAAAAAA+P////8/AAAAAACA////BwAAAAAAAAA4AEAA8B8AAAAA",
      "AAAAAAAAAAAAAAAA+P//////AQAAAAAA////BwAAAAAAAAAwAEAAxD8NAAAAAAAAAAAAAAAAAAAA+P//////AQAAAAAA////BwAA",
      "AAAAAADAAQAAxP+AAAAAAAAAAAAAAAAAAAAA8P//////AQAAAAAA/v//BwAAAAAAAACAHwAAwH8ABAAAAAAAAAAAAAAAAAAA4P//",
      "////AQAAAAAA/v//BwAAAAAAAAAAQFYEAMcAAAAAAAAAAAAAAAAAAAAA4P//////AAAAAAAA/v//DwAAAAAAAAAAAAgBAIABMAAA",
      "AAAAAAAAAAAAAAAAwP//////AAAAAAAA/v//DwAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAgP////9/AAAAAAAA/P//DwAA",
      "AAAAAAAAAAAAAQQAAAAAAAAAAAAAAAAAAAAAgP////8/AAAAAAAA/v//HyAAAAAAAAAAAACAHwQAAAAAAAAAAAAAAAAAAAAAAP//",
      "//8fAAAAAAAA/v//HyAAAAAAAAAAAADADwwAAAAAAAAAAAAAAAAAAAAAAP////8fAAAAAAAA////HzAAAAAAAAAAAADsDxwAAAAA",
      "AAAAAAAAAAAAAAAAAP7///8fAAAAAAAA////DzgAAAAAAAAAAAD/DxwAAAAA////////////////////////////////////////",
      "//////////////////9/AAAAAAAAAAAAAAAAAOD///8fAAAAAAAA////Ax8AAAAAAAAAAMD//z4AAABAAAAAAAAAAAAAAAAAAMD/",
      "//8PAAAAAAAA////AB8AAAAAAAAAAMD//z8AAAAAAAAAAAAAAAAAAAAAAMD///8PAAAAAAAA/v9/AB8AAAAAAAAAAOD//38AAAAA",
      "AAAAAAAAAAAAAAAAAMD///8PAAAAAAAA/v9/AB8AAAAAAAAAAPz///8BAAEAAAAAAAAAAAAAAAAAAMD///8HAAAAAAAA/P9/gA8A",
      "AAAAAAAAgP////8BAAIAAAAAAAAAAAAAAAAAAMD///8DAAAAAAAA/P//gA8AAAAAAAAAwP////8DAAAAAAAAAAAAAAAAAAAAAMD/",
      "/38AAAAAAAAA/P9/AA8AAAAAAAAAwP////8HAAAAAAAAAAAAAAAAAAAAAOD//x8AAAAAAAAA+P9/AAcAAAAAAAAA4P////8PAAAA",
      "AAAAAAAAAAAAAAAAAOD//w8AAAAAAAAA+P8fAAIAAAAAAAAAwP////8fAAAAAAAAAAAAAAAAAAAAAOD//wcAAAAAAAAA+P8fAAAA",
      "AAAAAAAA4P////8fAAAAAAAAAAAAAAAAAAAAAOD//wcAAAAAAAAA+P8fAAAAAAAAAAAAwP////8fAAAAAAAAAAAAAAAAAAAAAOD/",
      "/wcAAAAAAAAA8P8PAAAAAAAAAAAAgP////8/AAAAAAAAAAAAAAAAAAAAAOD//wMAAAAAAAAA4P8HAAAAAAAAAAAAgP////8fAAAA",
      "AAAAAAAAAAAAAAAAAPD//wMAAAAAAAAA4P8HAAAAAAAAAAAAgP////8fAAAAAAAAAAAAAAAAAAAAAPD//wEAAAAAAAAAwP8DAAAA",
      "AAAAAAAAAP////8fAAAAAAAAAAAAAAAAAAAAAOD//wAAAAAAAAAAwP8BAAAAAAAAAAAAAP8D/P8PAAAAAAAAAAAAAAAAAAAAAPD/",
      "fwAAAAAAAAAAwH8AAAAAAAAAAAAAgP8A2P8HAAAAAAAAAAAAAAAAAAAAAPD/OwAAAAAAAAAAgAEAAAAAAAAAAAAAAAcA6P8HAAAA",
      "AAAAAAAAAAAAAAAAAPj/BwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwP8DAAACAAAAAAAAAAAAAAAAAPj/BwAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAP8DAAAEAAAAAAAAAAAAAAAAAPz/BwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAP8DAAAIAAAAAAAAAAAAAAAAAPj/",
      "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGgAAAA4AAAAAAAAAAAAAAAAAPg/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAc",
      "AAAAAAAAAAAAAAAAAPw/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAYAAAAAAAAAAAAAAAAAPwHAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAOAAAAALAAAAAAAAAAAAAAAAAPwfAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAOAAAIADAAAAAAAAAAAAAAAAAPgH",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAMABAAAAAAAAAAAAAAAAAPwHAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAHAA",
      "AAAAAAAAAAAAAAAAAP4BAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAHgAAAAAAAAAAAAAAAAAAP4BAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAADAAAAAAAAAAAAAAAAAAAP4DAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAP8B",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAP8AAAAAAAAAAAAAAAAAAAAAAAIAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAH4AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAH6AAwAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPwA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPADAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "BgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACAAAAAAAAAAAAAAAAAAAAAAAAAAOAAAAAAAAAAAAAAAAAIAf",
      "AAAAAAAeeADADwAAAAAAAAAAAAAAAAAAAAAAAAAHAAAAAAAAAAAAAAAAAPD/AADA/////////z8AAAAAAAAAAAAAAAAAAAAAAAAH",
      "AAAAAAAAAAAAAAAAgP///wH4//////////8DAAAAAAAAAAAAAAAAAAAAADAfAAAAAAAAAAAAAADw8////wP+////////////AwAA",
      "AAAAAAAAAAAAAAAAAHA/AAAAAAAAAAA8//P//////+D/////////////HwAAAAAAAAAAAAAAAAAAAP5+AAAAAAAA5v//////////",
      "//D//////////////38AAAAAAAAAAAAAAAAAAAB+AAAAAACA/////////////////////////////38AAAAAAAAAYAYA4ON/OPh/",
      "AAAAAADw/////////////////////////////x8AAAAAAADwv/MXgP////8fAAAAAADw/////////////////////////////wMA",
      "AAAAAPz///////////8HAAAAAAD+/////////////////////////////wAAAAAAAPz//////////z8AAAAAAP//////////////",
      "/////////////////wAAAADA/////////////wEAAAAA8P///////////////////////////////wAAAACH////////////PwAA",
      "APgA/////////////////////////////////wcAAAAcgP//////////fwAAAP4BwP//////////////////////////////HwAA",
      "AAAAAP7//////////wf8wD8AwP//////////////////////////////DwAAAAAA/v////////////8HAAD4////////////////",
      "////////////////HwAAAAAA+P//////////////4f///////////////////////////////////wAAAAAA/P//////////////",
      "/////////////////////////////////////x8A3x8AAP7/////////////////////////////////////////////fwAAAAAA",
      "AAAAgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
    ].join("");
    const LAND_BITS = Uint8Array.from(atob(LAND_MASK), (c) => c.codePointAt(0));
    function onLand(latDeg, lonDeg) {
      const row = Math.min(179, Math.max(0, Math.floor(90 - latDeg)));
      const col = Math.min(359, Math.max(0, Math.floor(lonDeg + 180)));
      const i = row * 360 + col;
      return ((LAND_BITS[i >> 3] >> (i & 7)) & 1) === 1;
    }
    function dottedNetwork() {
      const positions = [];
      const facets = [];
      const bands = 100;
      for (let band = 1; band < bands; band++) {
        const latDeg = 90 - (band / bands) * 180;
        const count = Math.max(16, Math.round(Math.cos(THREE.MathUtils.degToRad(latDeg)) * 200));
        for (let i = 0; i < count; i++) {
          const lonDeg = (i / count) * 360 - 180;
          const p = latLon(latDeg, lonDeg, RADIUS);
          positions.push(p.x, p.y, p.z);
          facets.push(onLand(latDeg, lonDeg) ? 1 : 0);
        }
      }
      const geometry = new THREE.BufferGeometry();
      geometry.setAttribute("position", new THREE.Float32BufferAttribute(positions, 3));
      geometry.setAttribute("land", new THREE.Float32BufferAttribute(facets, 1));
      return new THREE.Points(geometry, new THREE.ShaderMaterial({
        transparent: true,
        depthWrite: false,
        vertexShader: [
          "attribute float land;",
          "varying float vFacing; varying float vLand;",
          "void main() {",
          "  vec4 mv = modelViewMatrix * vec4(position, 1.0);",
          "  vec3 n = normalize(mat3(modelViewMatrix) * position);",
          "  vFacing = clamp(dot(n, vec3(0.05, 0.12, 1.0)), 0.0, 1.0);",
          "  vLand = land;",
          "  gl_PointSize = mix(0.9, 1.6, vFacing) + land * mix(1.0, 2.2, vFacing);",
          "  gl_Position = projectionMatrix * mv;",
          "}"
        ].join("\n"),
        fragmentShader: [
          "varying float vFacing; varying float vLand;",
          "void main() {",
          "  vec2 uv = gl_PointCoord - vec2(0.5);",
          "  float d = dot(uv, uv);",
          "  if (d > 0.25) discard;",
          "  float glow = smoothstep(0.25, 0.02, d);",
          "  float base = mix(0.03, 0.16, pow(vFacing, 1.4));",
          "  float bright = mix(0.14, 1.0, pow(vFacing, 1.1));",
          "  float alpha = glow * mix(base, bright, vLand);",
          "  vec3 color = mix(vec3(0.18, 0.46, 0.9), vec3(0.42, 0.86, 1.0), vLand);",
          "  gl_FragColor = vec4(color, alpha);",
          "}"
        ].join("\n")
      }));
    }
    function arcPoints(a, b) {
      const start = a.clone().normalize();
      const end = b.clone().normalize();
      const pts = [];
      const steps = 72;
      for (let i = 0; i <= steps; i++) {
        const t = i / steps;
        const v = new THREE.Vector3().copy(start).lerp(end, t).normalize();
        pts.push(v.multiplyScalar(RADIUS * 1.045 + Math.sin(Math.PI * t) * 0.28));
      }
      return pts;
    }
    // Link colours match the legend: cyan solid = reached, red dashed = timed out, grey dashed = silent.
    const LINK_STYLE = {
      synced: { color: 0x5ee7ff, dashed: false, opacity: 0.78 },
      online: { color: 0x5ee7ff, dashed: false, opacity: 0.42 },
      unreachable: { color: 0xff6b5e, dashed: true, opacity: 0.7 },
      offline: { color: 0x4a6273, dashed: true, opacity: 0.3 }
    };
    const linkFor = (peer, data) => {
      return (data.links || []).find((link) => link.name === peer.device_id || (peer.endpoint && link.name === peer.endpoint)) || null;
    }
    function linkState(peer, data) {
      if (peer.revoked) return "revoked";
      const link = linkFor(peer, data);
      if (link?.state === "ok") return "synced";
      if (link?.state === "timeout") return "unreachable";
      return alive(peer) ? "online" : "offline";
    }
    function arcBetween(a, b, style) {
      const pts = arcPoints(a, b);
      const geometry = new THREE.BufferGeometry().setFromPoints(pts);
      const material = style.dashed
        ? new THREE.LineDashedMaterial({ color: style.color, dashSize: 0.03, gapSize: 0.02, transparent: true, opacity: style.opacity })
        : new THREE.LineBasicMaterial({ color: style.color, transparent: true, opacity: style.opacity });
      const line = new THREE.Line(geometry, material);
      if (style.dashed) line.computeLineDistances();
      return { line, points: pts };
    }
    function ensureWorld() {
      if (!THREE) return null;
      if (world) return world;
      let renderer;
      try {
        renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
      } catch (err) {
        globalThis.console?.debug?.(err);
        return null;
      }
      renderer.setPixelRatio(Math.min(globalThis.devicePixelRatio || 1, 2));
      renderer.setClearColor(0x000000, 0);
      stage.replaceChildren(renderer.domElement);
      const scene = new THREE.Scene();
      const camera = new THREE.PerspectiveCamera(32, 1, 0.1, 40);
      camera.position.set(0, 0.04, 6.35);
      camera.lookAt(0, -0.06, 0);
      scene.add(new THREE.AmbientLight(0x6ea8c4, 0.62));
      const key = new THREE.DirectionalLight(0xe8f6ff, 1.35);
      key.position.set(-2.4, 1.8, 3.4);
      scene.add(key);
      const rim = new THREE.DirectionalLight(0x00b4ff, 0.85);
      rim.position.set(2.2, -0.6, -1.4);
      scene.add(rim);
      const globe = new THREE.Group();
      globe.add(dottedNetwork());
      const markers = new THREE.Group();
      const arcs = new THREE.Group();
      globe.add(markers);
      globe.add(arcs);
      scene.add(globe);
      const raycaster = new THREE.Raycaster();
      const pointer = new THREE.Vector2();
      renderer.domElement.addEventListener("pointerdown", (ev) => {
        dragging = true;
        moved = false;
        world.spinTarget = null;
        lastX = ev.clientX;
        lastY = ev.clientY;
        renderer.domElement.classList.add("dragging");
      });
      globalThis.addEventListener("pointermove", (ev) => {
        if (!dragging) {
          aim(ev);
          return;
        }
        const dx = ev.clientX - lastX;
        const dy = ev.clientY - lastY;
        if (Math.abs(dx) + Math.abs(dy) > 3) moved = true;
        globe.rotation.y += dx * 0.005;
        globe.rotation.x = THREE.MathUtils.clamp(globe.rotation.x + dy * 0.0032, -0.55, 0.55);
        lastX = ev.clientX;
        lastY = ev.clientY;
      });
      globalThis.addEventListener("pointerup", () => {
        dragging = false;
        renderer.domElement.classList.remove("dragging");
      });
      renderer.domElement.addEventListener("click", (ev) => {
        ev.stopPropagation();
        if (moved) {
          moved = false;
          return;
        }
        const hit = pick(ev);
        if (!hit) {
          hidePopover();
          return;
        }
        selectDevice(hit.object.userData.peer);
      });
      function aim(ev) {
        const hit = pick(ev);
        renderer.domElement.style.cursor = hit ? "pointer" : "grab";
      }
      function pick(ev) {
        const rect = renderer.domElement.getBoundingClientRect();
        pointer.x = ((ev.clientX - rect.left) / rect.width) * 2 - 1;
        pointer.y = -((ev.clientY - rect.top) / rect.height) * 2 + 1;
        raycaster.setFromCamera(pointer, camera);
        const hits = raycaster.intersectObjects(stations.map((s) => s.hit), false);
        return hits[0] || null;
      }
      world = { renderer, scene, camera, globe, markers, arcs, raycaster, links: [] };
      resize();
      requestAnimationFrame(tick);
      return world;
    }
    function resize() {
      if (!world) return;
      const w = stage.clientWidth;
      const h = stage.clientHeight;
      if (w < 2 || h < 2) return;
      const aspect = w / Math.max(h, 1);
      world.camera.aspect = aspect;
      // Portrait screens pull the camera back so the globe stays inside the viewport, above the dock.
      world.camera.position.set(0, 0.04, 6.35 * Math.max(1, 0.92 / aspect));
      world.camera.lookAt(0, -0.06, 0);
      world.camera.updateProjectionMatrix();
      world.renderer.setSize(w, h, false);
    }
    const disposeGroup = (group) => {
      while (group.children.length) {
        const child = group.children[0];
        group.remove(child);
        child.traverse((obj) => {
          if (obj.geometry) obj.geometry.dispose();
          if (obj.material) {
            const mats = Array.isArray(obj.material) ? obj.material : [obj.material];
            mats.forEach((mat) => mat.dispose());
          }
        });
      }
    }
    function roster(data) {
      const circle = (data.circles || [])[0];
      const members = (circle?.members.slice()) || [];
      if (!members.some((peer) => peer.self)) {
        members.unshift({
          device_id: data.device_id || "self",
          name: data.hostname || "this station",
          hostname: data.hostname || "",
          self: true,
          endpoint: data.listen || ""
        });
      }
      return members;
    }
    function packetMesh(color) {
      return new THREE.Mesh(
        new THREE.SphereGeometry(0.02, 12, 12),
        new THREE.MeshBasicMaterial({ color })
      );
    }
    function clipText(text, max) {
      const value = tidyLine(text);
      return value.length > max ? value.slice(0, max - 1).trimEnd() + "…" : value;
    }
    // A caption that rides with a packet and says what it carries.
    function caption(kind, text) {
      const cap = el("div", "flow " + kind);
      cap.append(el("i"), el("span", "", text));
      cap.style.visibility = "hidden";
      labels.appendChild(cap);
      return cap;
    }
    // What travels on a link: sync pages always, plus the latest decision and handoff; a probe that stops short when timed out.
    function linkPackets(peer, data, state, index) {
      if (state === "unreachable") {
        return [{ kind: "probe", color: 0xff6b5e, text: "Timed out", phase: index * 0.17, speed: 0.00016, limit: 0.55 }];
      }
      if (state !== "synced" && state !== "online") return [];
      const link = linkFor(peer, data);
      const packets = [{
        kind: "sync", color: 0x9af0ff, phase: index * 0.17, speed: 0.00012,
        text: link?.packets ? "Sync · " + plural(link.packets, "packet") : "Sync"
      }];
      const decision = (data.decisions || [])[0];
      if (decision) packets.push({ kind: "decision", color: 0xf5b942, phase: 0.45 + index * 0.1, speed: 0.00009, text: "Decision · " + clipText(decision, 36) });
      const handoff = (data.handoffs || [])[0];
      if (handoff) packets.push({ kind: "handoff", color: 0x4cd964, phase: 0.78 + index * 0.1, speed: 0.0001, text: "Handoff · " + clipText(handoff, 36) });
      return packets;
    }
    function addPacket(view, station, arc, spec) {
      const packet = packetMesh(spec.color);
      view.arcs.add(packet);
      view.links.push({
        points: arc.points, packet, phase: spec.phase, speed: spec.speed, limit: spec.limit || 1,
        cap: caption(spec.kind, spec.text), station: station.peer.device_id
      });
    }
    let captionStations = [];
    function captionAllowed(link) {
      if (selectedDevice) return link.station === selectedDevice;
      return captionStations.includes(link.station);
    }
    function outlined(geometry, fillColor, edgeColor) {
      const mesh = new THREE.Mesh(
        geometry,
        new THREE.MeshStandardMaterial({ color: fillColor, metalness: 0.45, roughness: 0.5, transparent: true, opacity: 0.9 })
      );
      mesh.add(new THREE.LineSegments(
        new THREE.EdgesGeometry(geometry),
        new THREE.LineBasicMaterial({ color: edgeColor, transparent: true, opacity: 0.85 })
      ));
      return mesh;
    }
    // A small open laptop drawn as a holographic outline: base, key rows, lid and a glowing screen.
    function deviceMesh(color, self) {
      const group = new THREE.Group();
      const base = outlined(new THREE.BoxGeometry(0.12, 0.007, 0.074), 0x0f2540, color);
      base.position.y = 0.0035;
      const keys = [];
      for (let row = 0; row < 3; row++) {
        const z = 0.028 - row * 0.014;
        keys.push(-0.045, 0.0075, z, 0.045, 0.0075, z);
      }
      const keyGeo = new THREE.BufferGeometry();
      keyGeo.setAttribute("position", new THREE.Float32BufferAttribute(keys, 3));
      const keyLines = new THREE.LineSegments(keyGeo, new THREE.LineBasicMaterial({ color, transparent: true, opacity: 0.35 }));
      const lid = new THREE.Group();
      lid.position.set(0, 0.007, -0.036);
      lid.rotation.x = -0.3;
      const back = outlined(new THREE.BoxGeometry(0.12, 0.082, 0.005), 0x0f2540, color);
      back.position.y = 0.041;
      const screen = new THREE.Mesh(
        new THREE.PlaneGeometry(0.106, 0.068),
        new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.9 })
      );
      screen.position.set(0, 0.042, 0.003);
      lid.add(back, screen);
      group.add(base, keyLines, lid);
      const scale = self ? 1.35 : 1.15;
      group.scale.set(scale, scale, scale);
      return { mesh: group, screen };
    }
    const HERO_ANGLE = THREE ? new THREE.Quaternion().setFromEuler(new THREE.Euler(-0.3, 0.3, 0)) : null;
    const tmpQuat = THREE ? new THREE.Quaternion() : null;
    // Keep the laptop facing the camera at a three-quarter angle wherever it sits on the globe.
    function faceCamera(station) {
      station.group.getWorldQuaternion(tmpQuat);
      station.device.quaternion.copy(tmpQuat.invert()).multiply(world.camera.quaternion).multiply(HERO_ANGLE);
    }
    function orientOnSurface(object, pos) {
      const up = pos.clone().normalize();
      const forward = new THREE.Vector3(0, 0, 1).addScaledVector(up, -up.z);
      if (forward.lengthSq() < 1e-4) forward.set(1, 0, 0);
      forward.normalize();
      const right = new THREE.Vector3().crossVectors(up, forward).normalize();
      object.quaternion.setFromRotationMatrix(new THREE.Matrix4().makeBasis(right, up, forward));
      object.position.copy(pos);
    }
    function syncStations(data) {
      const view = ensureWorld();
      if (!view) return;
      const members = roster(data);
      const placed = placeMembers(members);
      disposeGroup(view.markers);
      disposeGroup(view.arcs);
      labels.replaceChildren();
      stations = placed.map((pt) => {
        let color = 0x5ee7ff;
        if (pt.peer.revoked) {
          color = 0x8a3b32;
        } else if (pt.peer.self) {
          color = 0x00b4ff;
        } else if (!alive(pt.peer)) {
          color = 0x4a6273;
        }
        const pos = latLon(pt.lat, pt.lon, RADIUS * 1.01);
        const group = new THREE.Group();
        orientOnSurface(group, pos);
        const device = deviceMesh(color, pt.peer.self);
        device.mesh.position.y = 0.13;
        const pin = new THREE.Mesh(
          new THREE.CylinderGeometry(0.0025, 0.0025, 0.1, 6),
          new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.55 })
        );
        pin.position.y = 0.05;
        const ring = new THREE.Mesh(
          new THREE.RingGeometry(0.05, 0.058, 48),
          new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.7, side: THREE.DoubleSide })
        );
        ring.rotation.x = -Math.PI / 2;
        ring.position.y = 0.002;
        const hit = new THREE.Mesh(
          new THREE.SphereGeometry(0.12, 10, 10),
          new THREE.MeshBasicMaterial({ visible: false })
        );
        hit.position.y = 0.12;
        hit.userData.peer = pt.peer;
        group.add(device.mesh, pin, ring, hit);
        view.markers.add(group);
        const tag = document.createElement("div");
        tag.className = ("tag " + stateClass(pt.peer)).trim();
        const row = document.createElement("div");
        row.className = "tag-row";
        const strong = document.createElement("strong");
        strong.textContent = computerLabel(pt.peer, data);
        const led = document.createElement("i");
        led.className = ("led " + ledClass(pt.peer)).trim();
        row.append(icon("iconComputer"), strong, led);
        const em = document.createElement("em");
        em.textContent = peerDetails(pt.peer, data).join("  ·  ");
        tag.append(row, em);
        labels.appendChild(tag);
        return { peer: pt.peer, group, ring, device: device.mesh, screen: device.screen, hit, tag };
      });
      const self = stations.find((s) => s.peer.self) || stations[0];
      view.links = [];
      const others = stations.filter((s) => s !== self);
      labels.classList.toggle("dense", others.length > 8);
      labels.classList.toggle("sparse", others.length > 16);
      others.forEach((s, index) => {
        if (!self) return;
        const state = linkState(s.peer, data);
        if (state === "revoked") return;
        const arc = arcBetween(self.group.position, s.group.position, LINK_STYLE[state]);
        view.arcs.add(arc.line);
        linkPackets(s.peer, data, state, index).forEach((spec) => addPacket(view, s, arc, spec));
      });
      captionStations = others.length > 8 ? [] : others.slice(0, 3).map((s) => s.peer.device_id);
      if (openStation && !stations.some((s) => s.peer.device_id === openStation)) hidePopover();
    }
    function project(object) {
      const v = object.getWorldPosition(new THREE.Vector3());
      const normal = v.clone().normalize();
      const toCam = world.camera.position.clone().sub(v).normalize();
      const facing = normal.dot(toCam) > -0.02;
      v.project(world.camera);
      const rect = world.renderer.domElement.getBoundingClientRect();
      return {
        x: (v.x * 0.5 + 0.5) * rect.width + rect.left,
        y: (-v.y * 0.5 + 0.5) * rect.height + rect.top,
        facing
      };
    }
    // Anchors the popover beside the station and points its arrow at it (left edge, or right edge when flipped).
    function localPoint(p) {
      const origin = labels.getBoundingClientRect();
      return { x: p.x - origin.left, y: p.y - origin.top, facing: p.facing };
    }
    function placePopover(x, y) {
      popover.hidden = false;
      const host = document.getElementById("topology") || document.body;
      const origin = host.getBoundingClientRect();
      const lx = x - origin.left;
      const ly = y - origin.top;
      const width = popover.offsetWidth || 312;
      const height = popover.offsetHeight || 200;
      const gap = 30;
      let left = lx + gap;
      let atRight = false;
      if (left + width > origin.width - 16) {
        left = lx - width - gap;
        atRight = true;
      }
      let top = ly - 36;
      const dockTop = dock.getBoundingClientRect().top;
      const floor = (dockTop ? dockTop - origin.top : origin.height) - 12;
      if (top + height > floor) top = floor - height;
      if (top < 16) top = 16;
      if (left < 16) left = 16;
      popover.classList.toggle("at-right", atRight);
      const arrowY = Math.min(height - 22, Math.max(22, ly - top));
      popover.style.setProperty("--ay", arrowY + "px");
      popover.style.left = left + "px";
      popover.style.top = top + "px";
    }
    function statusLine(peer) {
      if (peer.revoked) return "Revoked";
      if (peer.self) return "This station";
      const when = relativeTime(peer.last_seen);
      return (alive(peer) ? "Online" : "Offline") + (when ? " · " + when : "");
    }
    function fillPopover(peer) {
      const circle = (lastData.circles || [])[0];
      const tile = document.getElementById("popTile");
      tile.className = "device pop-tile " + deviceClasses(peer);
      tile.replaceChildren(macIcon());
      document.getElementById("popName").textContent = computerLabel(peer, lastData);
      document.getElementById("popStatus").replaceChildren(el("i", "led " + ledClass(peer)), document.createTextNode(statusLine(peer)));
      const facts = document.getElementById("popFacts");
      facts.replaceChildren();
      const fact = (label, value) => {
        if (value) facts.appendChild(listRow({ title: label, aside: [value], kv: true }));
      };
      fact("Endpoint", peer.endpoint || "Not announced");
      if (peer.self) fact("Platform", lastData.platform);
      fact("Seen from", peer.remote_addr);
      if (!peer.self) fact("Last seen", relativeTime(peer.last_seen) || "Never");
      fact("Device", peer.device_id);
      const box = document.getElementById("popFolders");
      box.replaceChildren(el("div", "group-title", "Shared folders"));
      const card = el("div", "card");
      const list = stationFolders(peer, circle);
      if (!list.length) card.appendChild(listRow({ title: "No shared folders yet", quiet: true }));
      list.forEach((path) => card.appendChild(listRow({ lead: icon("iconFolder"), title: folderName(path), detail: path })));
      box.appendChild(card);
    }
    function openFor(peer) {
      openStation = peer.device_id;
      fillPopover(peer);
      const station = stations.find((s) => s.peer.device_id === peer.device_id);
      if (!station) return;
      const p = project(station.group);
      placePopover(p.x, p.y);
    }
    function tick(now) {
      requestAnimationFrame(tick);
      if (!world) return;
      if (typeof world.spinTarget === "number") {
        const diff = ((world.spinTarget - world.globe.rotation.y + Math.PI) % (Math.PI * 2) + Math.PI * 2) % (Math.PI * 2) - Math.PI;
        world.globe.rotation.y += diff * 0.08;
        world.globe.rotation.x += (0 - world.globe.rotation.x) * 0.08;
        if (Math.abs(diff) < 0.002) world.spinTarget = null;
      } else if (!dragging && !openStation) {
        world.globe.rotation.y += 0.0007;
      }
      (world.links || []).forEach((link) => {
        if (!link.points.length) return;
        const u = ((now * link.speed + link.phase) % 1) / link.limit;
        const running = u <= 1;
        link.packet.visible = running;
        if (running) {
          const index = Math.min(link.points.length - 1, Math.floor(u * (link.points.length - 1)));
          link.packet.position.copy(link.points[index]);
        }
        if (!running || !captionAllowed(link)) {
          link.cap.style.visibility = "hidden";
          return;
        }
        const p = localPoint(project(link.packet));
        link.cap.style.left = p.x + "px";
        link.cap.style.top = p.y + "px";
        link.cap.classList.toggle("flip", p.x > labels.clientWidth * 0.7);
        link.cap.style.visibility = p.facing ? "visible" : "hidden";
      });
      stations.forEach((s, i) => {
        const wave = Math.sin(now * 0.0022 + i);
        const pulse = 1 + wave * 0.18;
        s.ring.scale.set(pulse, pulse, pulse);
        s.ring.material.opacity = 0.26 + (wave + 1) * 0.22;
        s.screen.material.opacity = 0.78 + Math.sin(now * 0.004 + i * 1.7) * 0.1 + Math.sin(now * 0.013 + i) * 0.04;
        faceCamera(s);
        const projected = project(s.group);
        const p = localPoint(projected);
        s.tag.style.left = p.x + "px";
        s.tag.style.top = p.y + "px";
        s.tag.classList.toggle("flip", p.x > labels.clientWidth * 0.62);
        s.tag.classList.toggle("selected", s.peer.device_id === selectedDevice);
        s.tag.style.visibility = p.facing ? "visible" : "hidden";
        if (openStation === s.peer.device_id && !popover.hidden) placePopover(projected.x, projected.y);
      });
      world.renderer.render(world.scene, world.camera);
    }
    let chipsMenuOpen = false;
    let chipsActiveIndex = -1;
    let chipsOptions = [];
    function chipsEls() {
      return {
        hidden: document.getElementById("chips"),
        button: document.getElementById("chipsButton"),
        label: document.getElementById("chipsLabel"),
        list: document.getElementById("chipsList")
      };
    }
    function closeChipsMenu() {
      const { list, button } = chipsEls();
      chipsMenuOpen = false;
      chipsActiveIndex = -1;
      if (list) {
        list.hidden = true;
        list.style.maxHeight = "";
        list.style.top = "";
        list.style.bottom = "";
        list.style.left = "";
        list.style.width = "";
        list.classList.remove("flip-above");
      }
      if (button) button.setAttribute("aria-expanded", "false");
    }
    function placeChipsMenu() {
      const { button, list } = chipsEls();
      if (!button || !list || list.hidden) return;
      const gap = 4;
      list.style.left = "";
      list.style.width = "";
      list.style.top = "";
      list.style.bottom = "";
      list.style.maxHeight = "";
      list.classList.remove("flip-above");
      list.style.top = "calc(100% + " + gap + "px)";
      const menuHeight = list.offsetHeight;
      const rect = button.getBoundingClientRect();
      const spaceBelow = globalThis.innerHeight - rect.bottom - gap;
      const spaceAbove = rect.top - gap;
      if (menuHeight > spaceBelow && menuHeight <= spaceAbove) {
        list.style.top = "";
        list.style.bottom = "calc(100% + " + gap + "px)";
        list.classList.add("flip-above");
      } else if (menuHeight > spaceBelow) {
        list.style.maxHeight = Math.max(96, spaceBelow) + "px";
      }
    }
    function focusChipsOption(index) {
      const { list } = chipsEls();
      if (!list) return;
      const items = [...list.querySelectorAll('[role="option"]')];
      if (!items.length) return;
      chipsActiveIndex = Math.max(0, Math.min(index, items.length - 1));
      const item = items[chipsActiveIndex];
      item.focus();
      item.scrollIntoView({ block: "nearest" });
    }
    function openChipsMenu() {
      const { button, list, hidden } = chipsEls();
      if (!button || !list || !chipsOptions.length) return;
      chipsMenuOpen = true;
      list.hidden = false;
      button.setAttribute("aria-expanded", "true");
      placeChipsMenu();
      const current = hidden ? hidden.value : "";
      const idx = chipsOptions.findIndex((opt) => opt.value === current);
      focusChipsOption(Math.max(idx, 0));
    }
    function setChipsValue(value, text, opts) {
      const silent = opts?.silent;
      const { hidden, label, list, button } = chipsEls();
      if (hidden) hidden.value = value || "";
      if (label) label.textContent = text || "All";
      if (list) {
        [...list.querySelectorAll('[role="option"]')].forEach((node) => {
          node.setAttribute("aria-selected", node.dataset.value === (value || "") ? "true" : "false");
        });
      }
      if (button) button.setAttribute("aria-expanded", "false");
      closeChipsMenu();
      if (!silent && hidden) hidden.dispatchEvent(new Event("change", { bubbles: true }));
    }
    function bindChipsPicker() {
      const { button, list } = chipsEls();
      if (!button || !list) return;
      button.addEventListener("click", (ev) => {
        ev.stopPropagation();
        if (chipsMenuOpen) closeChipsMenu();
        else openChipsMenu();
      });
      button.addEventListener("keydown", (ev) => {
        if (ev.key === "ArrowDown" || ev.key === "Enter" || ev.key === " ") {
          ev.preventDefault();
          if (!chipsMenuOpen) openChipsMenu();
          else if (ev.key === "ArrowDown") focusChipsOption(chipsActiveIndex + 1);
        } else if (ev.key === "Escape" && chipsMenuOpen) {
          ev.preventDefault();
          closeChipsMenu();
        }
      });
      list.addEventListener("keydown", (ev) => {
        if (ev.key === "Escape") {
          ev.preventDefault();
          closeChipsMenu();
          button.focus();
        } else if (ev.key === "ArrowDown") {
          ev.preventDefault();
          focusChipsOption(chipsActiveIndex + 1);
        } else if (ev.key === "ArrowUp") {
          ev.preventDefault();
          focusChipsOption(chipsActiveIndex - 1);
        } else if (ev.key === "Enter" || ev.key === " ") {
          ev.preventDefault();
          const opt = chipsOptions[chipsActiveIndex];
          if (opt) setChipsValue(opt.value, opt.label);
          else button.focus();
        }
      });
      drawer.addEventListener("click", (ev) => {
        if (!chipsMenuOpen) return;
        if (button.contains(ev.target) || list.contains(ev.target)) return;
        closeChipsMenu();
      });
      globalThis.addEventListener("resize", () => {
        if (chipsMenuOpen) placeChipsMenu();
      });
      globalThis.addEventListener(
        "scroll",
        () => {
          if (chipsMenuOpen) placeChipsMenu();
        },
        true
      );
    }
    function renderChips(members) {
      const { hidden, list } = chipsEls();
      if (!hidden) return;
      closeChipsMenu();
      const current = selectedDevice;
      chipsOptions = [{ value: "", label: "All" }].concat(
        members.map((peer) => ({
          value: peer.device_id || "",
          label: computerLabel(peer, lastData || {})
        }))
      );
      if (list) {
        list.replaceChildren();
        chipsOptions.forEach((opt) => {
          const row = el("li", "", opt.label);
          row.setAttribute("role", "option");
          row.dataset.value = opt.value;
          row.setAttribute("aria-selected", opt.value === current ? "true" : "false");
          row.tabIndex = -1;
          row.addEventListener("click", () => setChipsValue(opt.value, opt.label));
          list.appendChild(row);
        });
      }
      const values = chipsOptions.map((opt) => opt.value);
      const next = values.includes(current) ? current : "";
      const pick = chipsOptions.find((opt) => opt.value === next) || chipsOptions[0];
      setChipsValue(pick.value, pick.label, { silent: true });
    }
    const folderCount = (folder) => {
      return (folder.kinds || []).reduce((n, kind) => n + ((kind.items || []).length), 0);
    }
    function countContexts(network) {
      return (network.computers || []).reduce((total, computer) => {
        return total + (computer.tree || []).reduce((sum, folder) => sum + folderCount(folder), 0);
      }, 0);
    }
    function folderFold(folder) {
      const count = folderCount(folder);
      const fold = el("details", "fold");
      fold.open = count > 0;
      const summary = document.createElement("summary");
      summary.appendChild(listRow({
        lead: icon("iconFolder"),
        title: folder.name || folderName(folder.path),
        detail: folder.path,
        aside: [count ? plural(count, "context") : "Empty", el("i", "chev")]
      }));
      fold.appendChild(summary);
      const nest = el("div", "nest");
      const kinds = folder.kinds || [];
      if (!kinds.length) nest.appendChild(listRow({ title: "No contexts in this folder yet", quiet: true }));
      kinds.forEach((kind) => {
        nest.appendChild(el("div", "kind-head", kindLabel(kind.kind)));
        (kind.items || []).forEach((item) => {
          const when = relativeTime(item.updated_at);
          nest.appendChild(listRow({
            lead: dot(kind.kind),
            title: item.subject,
            detail: item.summary,
            clamp: true,
            aside: when ? [when] : []
          }));
        });
      });
      fold.appendChild(nest);
      return fold;
    }
    function renderTree(network) {
      const tree = document.getElementById("tree");
      tree.replaceChildren();
      const computers = network.computers || [];
      const total = countContexts(network);
      contextTotals = {
        items: total,
        folders: computers.reduce((n, computer) => n + (computer.tree || []).filter((folder) => folderCount(folder) > 0).length, 0)
      };
      document.getElementById("ctxCount").textContent = total ? String(total) : "";
      updateDrawerMeta();
      if (!computers.length) {
        tree.appendChild(blank("No Contexts", "Decisions, lessons and handoffs stored by machines in this circle will appear here."));
        return;
      }
      computers.forEach((computer) => {
        const group = el("div", "group");
        const title = el("div", "group-title");
        title.append(icon("iconComputer"), document.createTextNode(computer.name));
        if (computer.self) title.appendChild(el("span", "", "· this station"));
        group.appendChild(title);
        const card = el("div", "card");
        const folders = computer.tree || [];
        if (!folders.length) card.appendChild(listRow({ title: "No shared folders", quiet: true }));
        folders.forEach((folder) => card.appendChild(folderFold(folder)));
        group.appendChild(card);
        tree.appendChild(group);
      });
    }
    async function loadContexts() {
      renderChips(roster(lastData || {}));
      const query = selectedDevice ? ("?device=" + encodeURIComponent(selectedDevice)) : "";
      const res = await fetch("/api/contexts" + query, { cache: "no-store" });
      renderTree(await res.json());
    }
    function render(data) {
      lastData = data;
      syncStations(data);
      renderDock(data);
      renderFeed(data);
      if (openStation) {
        const station = stations.find((s) => s.peer.device_id === openStation);
        if (station) fillPopover(station.peer);
      }
    }
    async function refresh() {
      const res = await fetch("/api/peers", { cache: "no-store" });
      const data = await res.json();
      render(data);
      await loadContexts();
      if (!find.hidden) {
        await loadCatalog();
        renderFind(findSearch.value);
      }
      await refreshMesh(data);
    }
    popover.addEventListener("click", (ev) => ev.stopPropagation());
    globalThis.addEventListener("resize", () => {
      resize();
      if (lastData) renderDock(lastData);
    });

    const MESSAGES_OPEN_KEY = "messagesWidgetOpen";
    function setMessagesOpen(open, persist) {
      const panel = document.getElementById("messages");
      const app = document.getElementById("msgApp");
      if (!panel) return;
      panel.hidden = !open;
      panel.setAttribute("aria-hidden", open ? "false" : "true");
      if (app) app.setAttribute("aria-pressed", String(open));
      if (persist !== false) {
        try { sessionStorage.setItem(MESSAGES_OPEN_KEY, open ? "1" : "0"); } catch { /* private mode */ }
      }
    }
    function initMessagesPanel() {
      let open = false;
      try { open = sessionStorage.getItem(MESSAGES_OPEN_KEY) === "1"; } catch { /* private mode */ }
      setMessagesOpen(open, false);
    }
    function showMessagesPanel(opts) {
      setMessagesOpen(true);
      if (opts?.expand) meshChat.expand();
    }
    function closeMessages() {
      meshChat.collapse();
      const panel = document.getElementById("messages");
      if (!panel || panel.hidden) return;
      setMessagesOpen(false);
    }
    function closeNearby() {
      const panel = document.getElementById("nearby");
      if (!panel || panel.hidden) return;
      panel.hidden = true;
      document.getElementById("nearbyApp").setAttribute("aria-pressed", "false");
    }
    function closeSettings() {
      const panel = document.getElementById("settings");
      if (!panel || panel.hidden) return;
      panel.hidden = true;
      document.getElementById("settingsApp").setAttribute("aria-pressed", "false");
    }
    function closeApps() {
      closeMessages();
      closeNearby();
      closeSettings();
      liveSessions.hidePanel();
    }
    function showLiveTerminal() {
      hidePopover();
      closeStack();
      closeFind();
      closeNearby();
      closeSettings();
      if (drawerKind) toggleDrawer(drawerKind);
      liveSessions.showPanel();
      if (liveSessions.raise) liveSessions.raise();
      return liveSessions.refreshSessionList().then(() => {
        if (liveSessions.openTabCount() === 0) return liveSessions.ensureDefaultSession();
      });
    }
    function openTerminal() {
      const panel = document.getElementById("liveSessions");
      if (panel && !panel.hidden) {
        liveSessions.hidePanel();
        return;
      }
      showLiveTerminal();
    }
    function openMessages() {
      const panel = document.getElementById("messages");
      const visible = panel && !panel.hidden;
      if (visible && meshChat.isExpanded()) {
        meshChat.collapse();
        return;
      }
      hidePopover();
      closeStack();
      closeFind();
      closeNearby();
      closeSettings();
      if (drawerKind) toggleDrawer(drawerKind);
      setMessagesOpen(true);
      meshChat.expand();
      if (liveSessions.lowerUnlessPinned) liveSessions.lowerUnlessPinned();
    }
    function openNearby() {
      const panel = document.getElementById("nearby");
      if (!panel.hidden) {
        closeNearby();
        return;
      }
      hidePopover();
      closeStack();
      closeFind();
      closeMessages();
      closeSettings();
      if (drawerKind) toggleDrawer(drawerKind);
      panel.hidden = false;
      document.getElementById("nearbyApp").setAttribute("aria-pressed", "true");
    }
    function openSettings() {
      const panel = document.getElementById("settings");
      if (!panel.hidden) {
        closeSettings();
        return;
      }
      hidePopover();
      closeStack();
      closeFind();
      closeMessages();
      closeNearby();
      if (drawerKind) toggleDrawer(drawerKind);
      panel.hidden = false;
      document.getElementById("settingsApp").setAttribute("aria-pressed", "true");
    }
    function renderDiscovered() {
      const box = document.getElementById("discoveredPeers");
      if (!box) return;
      box.replaceChildren();
      if (!discovered.length) {
        if (nearbyHiddenCount() > 0) {
          box.appendChild(el("p", "hint", "All nearby devices are hidden."));
          const clear = el("button", "nearby-clear-hidden", "Clear hidden");
          clear.type = "button";
          clear.addEventListener("click", () => clearNearbyHidden());
          box.appendChild(clear);
          return;
        }
        box.appendChild(el("p", "hint", "No nearby devices outside the circle."));
        return;
      }
      discovered.forEach((peer) => box.appendChild(discoveredCell(peer)));
    }
    const inviteLimitText = (seconds) => {
      const minutes = Math.max(1, Math.round(Number(seconds) / 60));
      return "A connection expires " + minutes + " " + (minutes === 1 ? "minute" : "minutes") + " after you create it. An expired connection is refused.";
    }
    const formatWhen = (iso) => {
      const when = new Date(iso);
      if (Number.isNaN(when.getTime())) return "at the stated time";
      return when.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
    }
    async function loadInviteRule() {
      const rule = document.getElementById("inviteRule");
      if (!rule) return;
      try {
        const res = await fetch("/api/circle/invite", { cache: "no-store" });
        if (!res.ok) return;
        const body = await res.json();
        if (body.ttl_seconds) rule.textContent = inviteLimitText(body.ttl_seconds);
      } catch { /* the sheet keeps the plain fallback sentence */ }
    }
    async function copyInviteJSONFallback(text) {
      if (globalThis.navigator?.clipboard?.writeText) {
        try {
          await globalThis.navigator.clipboard.writeText(text);
          return true;
        } catch (err) {
          if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        }
      }
      return false;
    }
    async function readInviteResponse(res) {
      let body = {};
      const raw = await res.text();
      if (!raw) return body;
      try {
        body = JSON.parse(raw);
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      }
      return body;
    }

    async function copyInviteText(json) {
      try {
        await navigator.clipboard.writeText(json);
        return true;
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        return copyInviteJSONFallback(json);
      }
    }

    async function copyConnection() {
      const status = document.getElementById("joinStatus");
      if (status) status.textContent = "";
      try {
        const res = await fetch("/api/circle/invite", { method: "POST" });
        const body = await readInviteResponse(res);
        const invite = body.invite;
        if (!res.ok || !invite || typeof invite !== "object") {
          const message = body.error || (res.status === 403
            ? "Open the dashboard on this computer (127.0.0.1) to create a connection."
            : "Could not create a connection.");
          if (status) status.textContent = message;
          return;
        }
        const json = JSON.stringify(invite, null, 2);
        const manual = document.getElementById("connectionManual");
        if (manual) manual.open = true;
        const rule = document.getElementById("inviteRule");
        if (rule && body.ttl_seconds) rule.textContent = inviteLimitText(body.ttl_seconds);
        const copied = await copyInviteText(json);
        const when = formatWhen(body.expires_at);
        if (status) {
          status.textContent = (copied ? "Copied. It expires " : "Could not copy. It expires ") + when + ". An expired connection is refused.";
        }
        refresh();
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        if (status) status.textContent = "Could not create a connection.";
      }
    }
    function parseJoinPayload(raw) {
      try {
        let payload = JSON.parse(raw);
        if (payload?.invite && typeof payload.invite === "object") payload = payload.invite;
        return payload;
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        return null;
      }
    }

    async function joinConnection() {
      const status = document.getElementById("joinStatus");
      const field = document.getElementById("joinJSON");
      const raw = field ? field.value.trim() : "";
      if (!raw) {
        if (status) status.textContent = "Paste a connection first.";
        return;
      }
      const payload = parseJoinPayload(raw);
      if (!payload) {
        if (status) status.textContent = "bad request";
        return;
      }
      try {
        const { res, body } = await submitJoinPayload(payload);
        if (!res.ok) {
          if (status) status.textContent = body.error || "Could not add this device.";
          return;
        }
        if (field) field.value = "";
        if (status) status.textContent = "This device joined " + (body.name || "the circle") + ".";
        refresh();
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        if (status) status.textContent = "Could not add this device.";
      }
    }
    function renderSettings(peers) {
      const listen = document.getElementById("shareListen");
      const folders = document.getElementById("shareFolders");
      if (listen) listen.textContent = peers.listen || "Not listening";
      if (!folders) return;
      folders.replaceChildren();
      const circles = peers.circles || [];
      if (!circles.length) {
        folders.appendChild(el("li", "", "No shared folders"));
        return;
      }
      circles.forEach((circle) => {
        const list = circle.folders || [];
        if (!list.length) {
          folders.appendChild(el("li", "", (circle.name || "Circle") + ": no folders"));
          return;
        }
        list.forEach((folder) => folders.appendChild(el("li", "", (circle.name ? circle.name + ": " : "") + folder)));
      });
    }
    async function postMesh(path) {
      const res = await fetch(path, { method: "POST" });
      if (!res.ok) return;
      const peersRes = await fetch("/api/peers", { cache: "no-store" });
      if (peersRes.ok) await refreshMesh(await peersRes.json());
    }
    async function fetchCoreUiState() {
      try {
        const coreRes = await fetch("/api/core/ui", { cache: "no-store" });
        if (coreRes.ok) return await coreRes.json();
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      }
      return { mesh: "running" };
    }

    function applyMeshPauseBanner(core) {
      const paused = core.mesh === "paused";
      const flag = document.getElementById("barPaused");
      if (flag) flag.hidden = !paused;
      const state = document.getElementById("meshState");
      if (state) state.textContent = paused ? "Mesh paused" : "Mesh running";
    }

    async function fetchDiscoveredOutsideCircle(members) {
      const discRes = await fetch("/api/discovery/peers", { cache: "no-store" });
      if (!discRes.ok) return [];
      const body = await discRes.json();
      const found = body.peers || [];
      const known = new Set(members.map((peer) => peer.device_id));
      if (selfID) known.add(selfID);
      return found.filter((peer) => peer.device_id && !known.has(peer.device_id));
    }

    async function refreshMesh(peers) {
      try {
        const circle = (peers.circles || [])[0] || null;
        meshID = circle?.id ? circle.id : "";
        selfID = peers.device_id || "";
        const members = (circle?.members) || [];
        applyMeshPauseBanner(await fetchCoreUiState());
        renderSettings(peers);
        const conversations = await fetchMeshConversations(meshID);
        await meshChat.apply({ meshID, selfID, circle, conversations, data: peers });
        meshCall.apply({ meshID, selfID, incoming: peers.incoming_call || null });
        liveSessions.apply();
        const termCount = document.getElementById("termCount");
        if (termCount) {
          const n = Math.max(Number(peers.terminal_active_count) || 0, liveSessions.openTabCount());
          termCount.textContent = n > 0 ? String(n) : "";
        }
        syncTerminalComposer(liveSessions.openTabCount());
        discoveredOutsideCircle = await fetchDiscoveredOutsideCircle(members);
        syncDiscoveredFromOutsideCircle();
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        const node = document.getElementById("composerStatus");
        if (node) node.textContent = "Couldn't refresh";
      }
    }
    initMessagesPanel();
    const initialDrawer = location.hash.replace("#", "");
    if (DRAWER_TITLE[initialDrawer]) toggleDrawer(initialDrawer);
    else if (initialDrawer === "messages") showMessagesPanel({ expand: true });
    else if (initialDrawer === "call") meshCall.openFromHash();
    else if (initialDrawer === "live-sessions" || initialDrawer === "terminals") liveSessions.openFromHash();
    else if (initialDrawer === "nearby") openNearby();
    else if (initialDrawer === "settings") openSettings();
    loadInviteRule();
    refresh();
    setInterval(refresh, 3000);
    globalThis.overdriveResize = resize;
    globalThis.overdriveParkSheets = parkSheets;
    globalThis.overdriveShowLiveTerminal = showLiveTerminal;
    globalThis.overdriveLiveTerminalOpen = () => liveSessions.openTabCount() > 0;
    globalThis.overdriveShareLiveTerminal = () => liveSessions.shareActiveToChat();
    }

  
