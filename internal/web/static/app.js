// app.js — satelle project page interactivity (vanilla, no framework).
//
//  * Tabs       — show one panel at a time; active tab in the URL hash.
//  * Filter     — one shared component over every panel: a query box parsed into
//                 removable chips (status:/priority:/category:/tags:|tag: + free
//                 text) plus order:<field> client-side sort; status:active hides
//                 terminal rows by default.
//  * Expand     — click a row to fetch + reveal its detail + ledger timeline
//                 inline; preserved (and refreshed) across realtime refreshes.
//  * Realtime   — /events SSE doorbell; on a topic trigger (debounced) refetch
//                 that panel's rows AND any open expansion, so a story's progress
//                 and timeline update live. Detail pages live-refresh too.
(function () {
  "use strict";
  var TERMINAL = { done: 1, cancelled: 1 };
  var PANELS = ["stories", "tasks", "workflow", "docs"];
  // Panels of work items (status + priority + default sort). The workflow and
  // docs panels are read-only catalogs: free-text filter only, no status/order
  // default chips.
  function isItemPanel(panel) {
    var t = panel.dataset.topic;
    return t === "stories" || t === "tasks";
  }
  var FILTER_KEYS = { status: 1, priority: 1, category: 1, tags: 1, tag: 1 };
  var ORDER_FIELDS = { updated: 1, created: 1, priority: 1, status: 1, title: 1, id: 1, order: 1 };
  var DEFAULT_ORDER = "updated"; // applied when no explicit order: token (order:none opts out)
  var PRIORITY_RANK = { critical: 0, high: 1, medium: 2, low: 3 };

  function topicForKind(kind) { return kind === "task" ? "tasks" : "stories"; }

  function debounce(fn, ms) {
    var t = null;
    return function () {
      var args = arguments, self = this;
      if (t) clearTimeout(t);
      t = setTimeout(function () { t = null; fn.apply(self, args); }, ms);
    };
  }

  // ---- tabs ----------------------------------------------------------------
  function showTab(name) {
    if (PANELS.indexOf(name) === -1) name = "stories";
    document.querySelectorAll(".tab").forEach(function (t) {
      t.setAttribute("aria-selected", t.dataset.panel === name ? "true" : "false");
    });
    document.querySelectorAll(".panel").forEach(function (p) {
      p.classList.toggle("active", p.dataset.topic === name);
    });
  }
  function initTabs() {
    // Tabs are real <a href="#panel"> links (so the browser offers open-in-new-tab,
    // middle-click, and right-click open-in-new-window, and the active tab lives in
    // the URL). A normal click changes the hash without reloading; we mirror the
    // hash to the active panel here. Anchors preserve the query string, so the
    // filters (stored as ?<panel>=… below) survive a tab switch and a new-tab open.
    window.addEventListener("hashchange", function () {
      showTab((location.hash || "#stories").slice(1));
    });
    showTab((location.hash || "#stories").slice(1));
  }

  // syncFilterToURL writes a panel's filter query to the URL as ?<topic>=<value>
  // (replaceState, preserving the #tab hash) so a refresh restores the same list.
  function syncFilterToURL(panel) {
    var input = panel.querySelector(".filterbar input");
    if (!input || !panel.dataset.topic) return;
    var params = new URLSearchParams(location.search);
    var v = input.value.trim();
    if (v) params.set(panel.dataset.topic, v); else params.delete(panel.dataset.topic);
    var qs = params.toString();
    var base = location.href.split("?")[0].split("#")[0];
    history.replaceState(null, "", base + (qs ? "?" + qs : "") + location.hash);
  }

  // ---- filtering -----------------------------------------------------------
  function parseQuery(q) {
    var filters = [], free = [], order = "";
    (q || "").trim().split(/\s+/).forEach(function (part) {
      if (!part) return;
      var i = part.indexOf(":");
      var key = i > 0 ? part.slice(0, i).toLowerCase() : "";
      if (key === "order") { order = part.slice(i + 1).toLowerCase(); return; }
      if (i > 0 && FILTER_KEYS[key]) {
        var k = key === "tag" ? "tags" : key;
        filters.push({ key: k, vals: part.slice(i + 1).toLowerCase().split(",").filter(Boolean) });
        return;
      }
      free.push(part.toLowerCase());
    });
    return { filters: filters, order: order, free: free };
  }

  function rowMatches(row, parsed) {
    var hasStatus = false;
    for (var k = 0; k < parsed.filters.length; k++) {
      var t = parsed.filters[k];
      if (t.key === "status") {
        hasStatus = true;
        if (t.vals.indexOf("all") === -1 && t.vals.indexOf(row.dataset.status || "") === -1) return false;
      } else if (t.key === "tags") {
        var tags = (row.dataset.tags || "").toLowerCase().split(",");
        if (!t.vals.some(function (v) { return tags.indexOf(v) !== -1; })) return false;
      } else {
        var val = (row.dataset[t.key] || "").toLowerCase();
        if (t.vals.indexOf("all") === -1 && t.vals.indexOf(val) === -1) return false;
      }
    }
    if (!hasStatus && TERMINAL[row.dataset.status]) return false; // default status:active
    var search = (row.dataset.search || "").toLowerCase();
    return parsed.free.every(function (term) { return search.indexOf(term) !== -1; });
  }

  function sortKey(row, field) {
    if (field === "priority") {
      var p = row.dataset.priority || "";
      return String(p in PRIORITY_RANK ? PRIORITY_RANK[p] : 9);
    }
    if (field === "title") return row.dataset.title || "";
    if (field === "id") return row.dataset.expandUrl || "";
    if (field === "order") {
      // The drive sequence within a sprint/epic lives in an `order:<N>` tag (see the
      // satelle-story-classification principle). Extract its NUMBER and zero-pad to a
      // fixed width so the string comparator sorts numerically (1,2,…,10); a row with
      // no order tag returns a high sentinel so it sorts LAST.
      var tags = (row.dataset.tags || "").split(",");
      for (var i = 0; i < tags.length; i++) {
        if (tags[i].indexOf("order:") === 0) {
          var n = parseInt(tags[i].slice(6), 10);
          if (!isNaN(n)) return ("000000" + n).slice(-6);
        }
      }
      return "999999"; // no order tag → last
    }
    return row.dataset[field] || ""; // updated, created, status
  }

  function applyOrder(panel, order) {
    if (!order || !ORDER_FIELDS[order]) return;
    var holder = panel.querySelector("[data-rows]");
    if (!holder || holder.tagName !== "TBODY") return; // tables only
    var rows = [].slice.call(holder.querySelectorAll("tr.row"));
    var desc = order === "updated" || order === "created"; // newest first
    rows.sort(function (a, b) {
      var av = sortKey(a, order), bv = sortKey(b, order);
      if (av < bv) return desc ? 1 : -1;
      if (av > bv) return desc ? -1 : 1;
      // Explicit, deterministic tie-break: equal primary keys (e.g. two stories
      // sharing the same order:<N>) fall back to the row id ascending, so the
      // order does not depend on the browser's sort stability.
      var ai = sortKey(a, "id"), bi = sortKey(b, "id");
      return ai < bi ? -1 : ai > bi ? 1 : 0;
    });
    rows.forEach(function (r) { holder.appendChild(r); });
  }

  function renderChips(panel, parsed, input) {
    var box = panel.querySelector(".chips");
    if (!box) return;
    box.innerHTML = "";
    function chip(label, isDefault, onRemove) {
      var c = document.createElement("span");
      c.className = "fchip" + (isDefault ? " is-default" : "");
      c.appendChild(document.createTextNode(label));
      var b = document.createElement("button");
      b.type = "button"; b.textContent = "×"; b.setAttribute("aria-label", "remove " + label);
      b.addEventListener("click", onRemove);
      c.appendChild(b);
      box.appendChild(c);
    }
    parsed.filters.forEach(function (t) {
      chip(t.key + ":" + t.vals.join(","), false, function () {
        input.value = rebuild(parsed, t, null, false); applyFilter(panel);
      });
    });
    if (parsed.order) {
      chip("order:" + parsed.order, false, function () {
        input.value = rebuild(parsed, null, null, true); applyFilter(panel);
      });
    }
    parsed.free.forEach(function (term) {
      chip(term, false, function () {
        input.value = rebuild(parsed, null, term, false); applyFilter(panel);
      });
    });
    var hasStatus = parsed.filters.some(function (t) { return t.key === "status"; });
    if (!hasStatus && isItemPanel(panel)) {
      // Default: terminal rows hidden. Removing it reveals all (status:all).
      chip("status:active", true, function () {
        input.value = (input.value.trim() + " status:all").trim(); applyFilter(panel);
      });
    }
    if (!parsed.order && isItemPanel(panel)) {
      // Default sort surfaced as a chip, like status:active. Removing it opts out
      // of the default sort (order:none) rather than re-sorting.
      chip("order:updated", true, function () {
        input.value = (input.value.trim() + " order:none").trim(); applyFilter(panel);
      });
    }
    // Clear-all: one click back to defaults. Shown only when an explicit
    // filter/order/free-text token is set (nothing to clear on an empty input),
    // sitting in line with the default chips.
    if (input && input.value.trim() !== "") {
      var clr = document.createElement("button");
      clr.type = "button";
      clr.className = "fchip-clear";
      clr.textContent = "clear all";
      clr.setAttribute("aria-label", "clear all filters");
      clr.addEventListener("click", function () { input.value = ""; applyFilter(panel); });
      box.appendChild(clr);
    }
  }

  function rebuild(parsed, dropFilter, dropFree, dropOrder) {
    var parts = [];
    parsed.filters.forEach(function (t) { if (t !== dropFilter) parts.push(t.key + ":" + t.vals.join(",")); });
    if (parsed.order && !dropOrder) parts.push("order:" + parsed.order);
    parsed.free.forEach(function (f) { if (f !== dropFree) parts.push(f); });
    return parts.join(" ");
  }

  function applyFilter(panel) {
    var input = panel.querySelector(".filterbar input");
    var parsed = parseQuery(input ? input.value : "");
    collapseAll(panel);
    var total = 0, shown = 0;
    panel.querySelectorAll("[data-rows] .row, [data-rows] .doc").forEach(function (row) {
      var match = rowMatches(row, parsed);
      row.style.display = match ? "" : "none";
      total++;
      if (match) shown++;
    });
    applyOrder(panel, parsed.order || DEFAULT_ORDER); // explicit default sort, not incidental order
    if (input) renderChips(panel, parsed, input);
    var count = panel.querySelector(".filter-count");
    if (count) count.textContent = shown + " / " + total;
    syncFilterToURL(panel); // reflect the active filter in the URL (refresh-safe)
  }

  // addFilterToken appends a filter token (e.g. tags:epic:foo or category:bar)
  // to a panel's filter input and re-applies — the click-a-tag-chip path. Adding
  // a token already present is a no-op (deduped on the whitespace-split input).
  function addFilterToken(panel, token) {
    var input = panel.querySelector(".filterbar input");
    if (!input || !token) return;
    var parts = input.value.trim().split(/\s+/).filter(Boolean);
    if (parts.indexOf(token) === -1) {
      input.value = (input.value.trim() + " " + token).trim();
    }
    applyFilter(panel);
  }

  function initFilters() {
    // Restore each panel's filter from the URL on load (?<topic>=…), so a refresh
    // or an opened link lands on the same filtered list.
    var params = new URLSearchParams(location.search);
    document.querySelectorAll(".panel").forEach(function (panel) {
      var input = panel.querySelector(".filterbar input");
      if (input) {
        var fromURL = panel.dataset.topic ? params.get(panel.dataset.topic) : null;
        if (fromURL !== null) input.value = fromURL;
        input.addEventListener("input", function () { applyFilter(panel); });
      }
      applyFilter(panel);
    });
  }

  // ---- expand / collapse ---------------------------------------------------
  function collapseAll(panel) {
    panel.querySelectorAll("tr.row[aria-expanded='true']").forEach(collapseRow);
  }
  function collapseRow(row) {
    row.setAttribute("aria-expanded", "false");
    var next = row.nextElementSibling;
    if (next && next.classList.contains("expansion")) next.remove();
  }
  function expandRow(row) {
    if (row.getAttribute("aria-expanded") === "true") return;
    row.setAttribute("aria-expanded", "true");
    var exp = document.createElement("tr");
    exp.className = "expansion";
    var td = document.createElement("td");
    td.colSpan = row.children.length;
    td.innerHTML = '<div class="expbody loading">loading…</div>';
    exp.appendChild(td);
    row.parentNode.insertBefore(exp, row.nextSibling);
    fetch(row.dataset.expandUrl, { headers: { "X-Requested-With": "fetch" } })
      .then(function (r) { return r.text(); })
      .then(function (html) { td.innerHTML = html; enhanceWorkflowDiagrams(td); })
      .catch(function () { td.innerHTML = '<div class="expbody">failed to load</div>'; });
  }

  // enhanceWorkflowDiagrams wires the dependency-free SVG workflow diagram for
  // interactivity (sty_19b2107a) — progressive enhancement over the server-rendered
  // SVG (no JS ⇒ the plain, readable diagram still renders). Hovering or keyboard-
  // focusing a node highlights that node and every edge touching it (matched by the
  // data-from/data-to the server stamps) and dims the rest; clicking/activating a
  // node correlates the transition rows below (data-from/data-to on .wf-edge) and
  // scrolls the first match into view. Scoped to `container` so it works inside a
  // freshly-expanded fragment. No external graph library.
  function enhanceWorkflowDiagrams(container) {
    if (!container) return;
    [].slice.call(container.querySelectorAll(".wf-diagram")).forEach(function (svg) {
      if (svg.dataset.enhanced) return;
      svg.dataset.enhanced = "1";
      function setHi(state) {
        svg.querySelectorAll(".wf-dnode").forEach(function (g) {
          var on = g.dataset.state === state;
          g.classList.toggle("wf-hi", on);
          g.classList.toggle("wf-dim", !on);
        });
        svg.querySelectorAll(".wf-edge-path, .wf-edge-label").forEach(function (el) {
          var inc = el.dataset.from === state || el.dataset.to === state;
          el.classList.toggle("wf-hi", inc);
          el.classList.toggle("wf-dim", !inc);
        });
      }
      function clearHi() {
        svg.querySelectorAll(".wf-hi, .wf-dim").forEach(function (el) {
          el.classList.remove("wf-hi");
          el.classList.remove("wf-dim");
        });
      }
      function correlate(state) {
        var first = null;
        container.querySelectorAll(".wf-edges .wf-edge").forEach(function (li) {
          var hit = li.dataset.from === state || li.dataset.to === state;
          li.classList.toggle("wf-edge-hi", hit);
          if (hit && !first) first = li;
        });
        if (first && first.scrollIntoView) first.scrollIntoView({ block: "nearest" });
      }
      [].slice.call(svg.querySelectorAll(".wf-dnode[data-state]")).forEach(function (g) {
        var state = g.dataset.state;
        g.addEventListener("mouseenter", function () { setHi(state); });
        g.addEventListener("mouseleave", clearHi);
        g.addEventListener("focus", function () { setHi(state); });
        g.addEventListener("blur", clearHi);
        g.addEventListener("click", function () { correlate(state); });
        g.addEventListener("keydown", function (e) {
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); correlate(state); }
        });
      });

      // Gate labels reveal their FULL reviewer skill on click (sty_677c604c) —
      // the short label swaps with the <title> text and back.
      [].slice.call(svg.querySelectorAll(".wf-edge-label")).forEach(function (el) {
        var t = el.querySelector("title");
        if (!t) return;
        el.style.cursor = "pointer";
        el.addEventListener("click", function (e) {
          e.stopPropagation();
          var node = el.childNodes[el.childNodes.length - 1];
          if (!el.dataset.short) el.dataset.short = node.textContent;
          node.textContent = node.textContent === el.dataset.short ? t.textContent : el.dataset.short;
        });
      });

      // Pan (drag) + zoom (wheel) + reset (double-click) by viewBox manipulation
      // (sty_677c604c) — read-only navigation, no library. data-vb keeps the
      // original box for reset and for clamping the zoom range.
      var base = (svg.getAttribute("data-vb") || svg.getAttribute("viewBox")).split(" ").map(Number);
      var cur = base.slice();
      function applyVB() { svg.setAttribute("viewBox", cur.join(" ")); }
      function unitsPerPx() { return cur[2] / svg.getBoundingClientRect().width; }
      svg.addEventListener("wheel", function (e) {
        e.preventDefault();
        var f = e.deltaY > 0 ? 1.15 : 1 / 1.15;
        var nw = Math.min(Math.max(cur[2] * f, base[2] / 8), base[2] * 2);
        f = nw / cur[2];
        var r = svg.getBoundingClientRect();
        var px = cur[0] + (e.clientX - r.left) * (cur[2] / r.width);
        var py = cur[1] + (e.clientY - r.top) * (cur[3] / r.height);
        cur = [px - (px - cur[0]) * f, py - (py - cur[1]) * f, cur[2] * f, cur[3] * f];
        applyVB();
      }, { passive: false });
      var drag = null;
      svg.addEventListener("pointerdown", function (e) {
        if (e.target.closest(".wf-dnode") || e.target.closest(".wf-edge-label")) return;
        drag = { x: e.clientX, y: e.clientY, vx: cur[0], vy: cur[1] };
        try { svg.setPointerCapture(e.pointerId); } catch (_) { /* synthetic/untrusted pointer */ }
        svg.classList.add("wf-panning");
      });
      svg.addEventListener("pointermove", function (e) {
        if (!drag) return;
        var u = unitsPerPx();
        cur[0] = drag.vx - (e.clientX - drag.x) * u;
        cur[1] = drag.vy - (e.clientY - drag.y) * u;
        applyVB();
      });
      svg.addEventListener("pointerup", function () { drag = null; svg.classList.remove("wf-panning"); });
      svg.addEventListener("dblclick", function () { cur = base.slice(); applyVB(); });
    });

    // The cancel/recovery toggle: de-emphasised edges (wf-edge-alt) can be hidden
    // entirely so the spine reads clean. aria-pressed tracks state.
    [].slice.call(container.querySelectorAll(".wf-toggle-alt")).forEach(function (btn) {
      if (btn.dataset.enhanced) return;
      btn.dataset.enhanced = "1";
      btn.addEventListener("click", function () {
        var wrap = btn.closest(".expbody") || container;
        var on = btn.getAttribute("aria-pressed") !== "true";
        btn.setAttribute("aria-pressed", on ? "true" : "false");
        [].slice.call(wrap.querySelectorAll(".wf-diagram")).forEach(function (svg) {
          svg.classList.toggle("wf-hide-alt", !on);
        });
      });
    });
  }
  function toggleRow(row) {
    if (row.getAttribute("aria-expanded") === "true") collapseRow(row); else expandRow(row);
  }
  function copyId(el) {
    var id = el.dataset.id || el.textContent;
    function feedback() {
      el.classList.add("copied");
      el.textContent = "copied ✓";
      setTimeout(function () { el.textContent = id; el.classList.remove("copied"); }, 1000);
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(id).then(feedback, feedback);
    } else {
      feedback();
    }
  }

  function initExpand() {
    document.querySelectorAll(".panel").forEach(function (panel) {
      panel.addEventListener("click", function (e) {
        var chip = e.target.closest(".tagchip[data-filter]");
        if (chip) { e.preventDefault(); e.stopPropagation(); addFilterToken(panel, chip.dataset.filter); return; } // filter, don't toggle
        var idEl = e.target.closest(".id-copy");
        if (idEl) { e.preventDefault(); e.stopPropagation(); copyId(idEl); return; } // copy, don't toggle/navigate
        if (e.target.closest("a")) return; // let real links (e.g. Open story) through
        var row = e.target.closest("tr.row[data-expand-url]");
        if (row) toggleRow(row);
      });
      panel.addEventListener("keydown", function (e) {
        if (e.key !== "Enter" && e.key !== " ") return;
        // A tag chip is a native <button>: Enter/Space fires its click (handled
        // above). Bail so the row toggle below doesn't also fire.
        if (e.target.closest(".tagchip[data-filter]")) return;
        var idEl = e.target.closest(".id-copy");
        if (idEl) { e.preventDefault(); copyId(idEl); return; }
        var row = e.target.closest("tr.row[data-expand-url]");
        if (row) { e.preventDefault(); toggleRow(row); }
      });
    });
  }

  // ---- realtime ------------------------------------------------------------
  function refetchPanel(topic) {
    var panel = document.querySelector('.panel[data-topic="' + topic + '"]');
    if (!panel) return;
    var holder = panel.querySelector("[data-rows]");
    if (!holder) return;
    // Capture which rows are open so the swap doesn't collapse what the user
    // is reading; re-expand them afterwards (refreshing their live timeline).
    var openUrls = [].slice.call(panel.querySelectorAll('tr.row[aria-expanded="true"]'))
      .map(function (r) { return r.dataset.expandUrl; });
    fetch("fragment/" + topic)
      .then(function (r) { return r.text(); })
      .then(function (html) {
        holder.innerHTML = html;
        applyFilter(panel);
        openUrls.forEach(function (url) {
          var row = holder.querySelector('tr.row[data-expand-url="' + url + '"]');
          if (row && row.style.display !== "none") expandRow(row);
        });
        var tab = document.querySelector('.tab[data-panel="' + topic + '"] .n');
        if (tab) tab.textContent = panel.querySelectorAll("[data-rows] .row").length;
        if (topic === "stories") refreshBacklogBadge(panel);
      })
      .catch(function () {});
  }

  // refreshBacklogBadge recomputes the Stories tab's 'N backlog' badge from the
  // live rows (sty_af09a484). The server renders it at page load from BacklogCount,
  // but a realtime refetch only refreshed the total .n — so the badge stayed frozen
  // at the page-load value. Recompute from the rows carrying data-status="backlog"
  // and create / update / remove the badge consistently with the server template's
  // {{if .BacklogCount}} (shown when > 0, gone when 0). The badge's spacing comes
  // from its CSS margin, so no literal separator node is needed.
  function refreshBacklogBadge(panel) {
    var tabEl = document.querySelector('.tab[data-panel="stories"]');
    if (!tabEl) return;
    var n = panel.querySelectorAll('[data-rows] .row[data-status="backlog"]').length;
    var badge = tabEl.querySelector(".n-backlog");
    if (n > 0) {
      if (!badge) {
        badge = document.createElement("span");
        badge.className = "n-backlog";
        badge.title = "stories in the open backlog";
        tabEl.appendChild(badge);
      }
      badge.textContent = n + " backlog";
    } else if (badge) {
      badge.remove();
    }
  }

  // Panels with a rows fragment endpoint (the refetch targets); workflow has none.
  var LIVE_TOPICS = ["stories", "tasks", "docs"];

  // initLive wires realtime through ONE visibility-gated EventSource that serves
  // both consumers — the panel refetch (list pages) and the detail-fragment
  // refresh (detail pages). It is held ONLY while the tab is visible: browsers
  // cap HTTP/1.1 connections at ~6 PER HOST across all tabs, and a persistent SSE
  // per open tab starves that pool, so a REFRESH of the active tab can't get a
  // connection and hangs (sty_a4fc4d00). Closing the stream on a hidden tab (and
  // on pagehide) frees the slot; the on-reconnect reconcile keeps a returning tab
  // current. window.__satelleLive exposes the connection state for tests.
  function initLive() {
    if (!window.EventSource) return;
    // The ◐ brand mark carries the live SSE connection state as its COLOUR
    // (sty_cd2fe2f3): accent-green by default (connected), red via the 'sse-down'
    // class when the /events stream drops — added on close/error, removed on open.
    // Inverted from a positive 'connected' flag so a fresh render shows green with
    // no red flash before the first open. The uptime snapshot rides in the mark's
    // title tooltip (a server render-time value). (was the .uptime pill, sty_efeb2a69)
    var dot = document.querySelector(".brand-mark");
    var isProjects = document.body.getAttribute("data-page") === "projects";
    var detailEl = document.getElementById("detail-live");
    var detailKind = detailEl ? detailEl.dataset.kind : null;
    var detailId = detailEl ? detailEl.dataset.id : null;
    var detailTopic = detailEl ? topicForKind(detailKind) : null;

    var refetch = {}; // per-topic debounced panel refetch (built once, reused)
    LIVE_TOPICS.forEach(function (tp) { refetch[tp] = debounce(function () { refetchPanel(tp); }, 250); });
    var refreshDetail = detailEl ? debounce(function () {
      fetch("fragment/" + detailKind + "/" + detailId)
        .then(function (r) { return r.text(); })
        .then(function (html) { detailEl.innerHTML = html; })
        .catch(function () {});
    }, 250) : null;

    // reconcile pulls fresh state after ANY connection gap (reconnect, or a
    // reopen after hidden→visible) so nothing is missed while disconnected.
    function reconcile() {
      if (isProjects) { location.reload(); return; }
      LIVE_TOPICS.forEach(function (tp) { refetchPanel(tp); });
      if (refreshDetail) refreshDetail();
    }

    var src = null;
    // firstOpen is PAGE-lifetime (not per-EventSource): only the first open of a
    // freshly server-rendered page skips reconcile; every later open (SSE
    // auto-reconnect OR a reopen after the tab returns to visible) reconciles.
    var firstOpen = document.visibilityState === "visible";

    window.__satelleLive = { open: false, opens: 0 };

    function connectLive() {
      if (src || document.visibilityState !== "visible") return;
      src = new EventSource("events");
      window.__satelleLive.open = true;
      window.__satelleLive.opens++;
      src.addEventListener("open", function () {
        if (dot) dot.classList.remove("sse-down"); // connected → mark is accent-green
        if (firstOpen) { firstOpen = false; return; }
        reconcile();
      });
      src.addEventListener("trigger", function (ev) {
        if (refetch[ev.data]) refetch[ev.data]();
        if (refreshDetail && ev.data === detailTopic) refreshDetail();
        // The / landing has no panels — a "projects" doorbell (the served set
        // changed: workspace add/remove, a child failed) reloads it (sty_4ea4d4df).
        if (ev.data === "projects" && isProjects) location.reload();
      });
      src.onerror = function () { if (dot) dot.classList.add("sse-down"); }; // stream dropped → mark red
    }

    function disconnectLive() {
      if (!src) return;
      src.close();
      src = null;
      window.__satelleLive.open = false;
      if (dot) dot.classList.add("sse-down"); // holding no connection (invisible on a hidden tab; cleared on reopen)
      // A page that first loads hidden must reconcile on its first (deferred)
      // open, so it is not the skip-me first-open of a fresh render.
      firstOpen = false;
    }

    document.addEventListener("visibilitychange", function () {
      if (document.visibilityState === "visible") connectLive(); else disconnectLive();
    });
    // Free the slot promptly on navigation/close and before bfcache entry.
    window.addEventListener("pagehide", disconnectLive);
    // bfcache restore does not always fire visibilitychange — reopen on show.
    window.addEventListener("pageshow", function () {
      if (document.visibilityState === "visible") connectLive();
    });

    connectLive(); // opens only if visible; a background-opened tab waits for view
  }

  // ---- theme (light default, dark optional, persisted) --------------------
  function currentTheme() {
    return document.documentElement.getAttribute("data-theme") === "dark" ? "dark" : "light";
  }
  function applyTheme(theme, persist) {
    if (theme === "dark") document.documentElement.setAttribute("data-theme", "dark");
    else document.documentElement.removeAttribute("data-theme"); // light is the default (no attr)
    try { localStorage.setItem("satelle-theme", theme); } catch (e) {}
    var btn = document.getElementById("theme-toggle");
    if (btn) btn.textContent = theme === "dark" ? "☀" : "☾"; // ☾ in light (→dark), ☀ in dark (→light); ◐ is the brand mark only
    // Persist the choice to the machine-wide config so it follows the operator
    // into every repo (best-effort; localStorage remains the fast-path cache).
    if (persist) {
      try { fetch("theme", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: "theme=" + theme }); } catch (e) {}
    }
  }
  function initTheme() {
    // The <head> script already applied any saved choice pre-paint; sync the
    // toggle label and wire the control. Default stays light when unset.
    applyTheme(currentTheme());
    var btn = document.getElementById("theme-toggle");
    if (btn) btn.addEventListener("click", function () {
      applyTheme(currentTheme() === "dark" ? "light" : "dark", true);
    });
  }

  // Attached documents are now a native <details> list (sty_1a239b4d) — no JS
  // needed; the disclosure works without a handler and survives live re-renders.

  // Project switcher (sty_2bc00a9d): the breadcrumb <details> dropdown works without
  // JS (native disclosure + tabbable links); this only adds the expected dropdown
  // ergonomics — close on outside click and on Escape (returning focus to summary).
  function initProjectSwitcher() {
    document.addEventListener("click", function (e) {
      document.querySelectorAll("details.proj-switch[open]").forEach(function (d) {
        if (!d.contains(e.target)) d.removeAttribute("open");
      });
    });
    document.addEventListener("keydown", function (e) {
      if (e.key !== "Escape") return;
      document.querySelectorAll("details.proj-switch[open]").forEach(function (d) {
        d.removeAttribute("open");
        var s = d.querySelector("summary");
        if (s) s.focus();
      });
    });
  }

  // Account menu (sty_2faa7dd4): the "Remove server" quick action clears the global
  // [hosted] server. The backend is the existing global-settings POST (a blank
  // `server` calls ClearGlobalHostedServer), gated by loopback + the X-Satelle-Settings
  // CSRF header — which a bare <form> POST cannot set, so intercept the submit and
  // fetch it (mirroring the global-settings form). A relative URL so the page's <base>
  // resolves the /slug/ prefix on a supervised child. On success reload: clearing the
  // server drops the signed-in identity, so the topbar must repaint.
  function initAccountMenu() {
    document.addEventListener("submit", function (e) {
      var form = e.target.closest ? e.target.closest(".acct-remove-form") : null;
      if (!form) return;
      e.preventDefault();
      fetch("settings/global", { method: "POST", headers: { "X-Satelle-Settings": "1" }, body: new FormData(form) })
        .then(function (r) { if (r.ok) location.reload(); })
        .catch(function () {});
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    initTheme();
    initTabs();
    initExpand();
    initFilters();
    initLive(); // one visibility-gated SSE serves both panels and detail
    initProjectSwitcher();
    initAccountMenu();
    enhanceWorkflowDiagrams(document); // any diagram already in the server-rendered page
  });
})();
