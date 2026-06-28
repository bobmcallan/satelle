// app.js — satelle project page interactivity (vanilla, no framework).
//
//  * Tabs       — show one panel at a time; active tab in the URL hash + crumb.
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
  var ORDER_FIELDS = { updated: 1, created: 1, priority: 1, status: 1, title: 1, id: 1 };
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
    var crumb = document.getElementById("crumb-tab");
    if (crumb) crumb.textContent = name;
  }
  function initTabs() {
    document.querySelectorAll(".tab").forEach(function (t) {
      t.addEventListener("click", function () {
        history.replaceState(null, "", "#" + t.dataset.panel);
        showTab(t.dataset.panel);
      });
    });
    showTab((location.hash || "#stories").slice(1));
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
      return 0;
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
  }

  function initFilters() {
    document.querySelectorAll(".panel").forEach(function (panel) {
      var input = panel.querySelector(".filterbar input");
      if (input) input.addEventListener("input", function () { applyFilter(panel); });
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
      .then(function (html) { td.innerHTML = html; })
      .catch(function () { td.innerHTML = '<div class="expbody">failed to load</div>'; });
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
        var idEl = e.target.closest(".id-copy");
        if (idEl) { e.preventDefault(); e.stopPropagation(); copyId(idEl); return; } // copy, don't toggle/navigate
        if (e.target.closest("a")) return; // let real links (e.g. Open story) through
        var row = e.target.closest("tr.row[data-expand-url]");
        if (row) toggleRow(row);
      });
      panel.addEventListener("keydown", function (e) {
        if (e.key !== "Enter" && e.key !== " ") return;
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
    fetch("/fragment/" + topic)
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
      })
      .catch(function () {});
  }

  // Panels with a rows fragment endpoint (the refetch targets); workflow has none.
  var LIVE_TOPICS = ["stories", "tasks", "docs"];

  function initLive() {
    if (!window.EventSource) return;
    var dot = document.querySelector(".uptime");
    var src = new EventSource("/events");
    var refetch = {}; // per-topic debounced refetch
    LIVE_TOPICS.forEach(function (tp) { refetch[tp] = debounce(function () { refetchPanel(tp); }, 250); });
    var firstOpen = true;
    src.addEventListener("open", function () {
      if (dot) dot.classList.add("on");
      // Durability: on every RE-connect, reconcile every panel — any CLI update
      // missed during a connection gap (reconnect, server restart, a dropped
      // trigger) is picked up here, so the page is eventually consistent with the
      // store even when a doorbell is lost. The first open is skipped: the page
      // was just server-rendered fresh, so a refetch would be redundant (and
      // would disrupt an expansion opened immediately after load).
      if (firstOpen) { firstOpen = false; return; }
      LIVE_TOPICS.forEach(function (tp) { refetchPanel(tp); });
    });
    src.addEventListener("trigger", function (ev) { if (refetch[ev.data]) refetch[ev.data](); });
    src.onerror = function () { if (dot) dot.classList.remove("on"); };
  }

  // ---- detail page live ----------------------------------------------------
  function initDetailLive() {
    var el = document.getElementById("detail-live");
    if (!el || !window.EventSource) return;
    var kind = el.dataset.kind, id = el.dataset.id, topic = topicForKind(kind);
    var dot = document.querySelector(".uptime");
    var src = new EventSource("/events");
    var refresh = debounce(function () {
      fetch("/fragment/" + kind + "/" + id)
        .then(function (r) { return r.text(); })
        .then(function (html) { el.innerHTML = html; })
        .catch(function () {});
    }, 250);
    var firstOpen = true;
    src.addEventListener("open", function () {
      if (dot) dot.classList.add("on");
      if (firstOpen) { firstOpen = false; return; } // already server-rendered fresh
      refresh(); // reconcile the detail on every RE-connect (durability)
    });
    src.addEventListener("trigger", function (ev) { if (ev.data === topic) refresh(); });
    src.onerror = function () { if (dot) dot.classList.remove("on"); };
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
    if (btn) btn.textContent = theme === "dark" ? "☀" : "◐";
    // Persist the choice to the machine-wide config so it follows the operator
    // into every repo (best-effort; localStorage remains the fast-path cache).
    if (persist) {
      try { fetch("/theme", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: "theme=" + theme }); } catch (e) {}
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

  // Story document tabs: delegated so it survives detail-page live re-renders.
  function initDocTabs() {
    document.addEventListener("click", function (e) {
      var tab = e.target.closest(".doc-tab");
      if (!tab) return;
      var wrap = tab.closest(".doc-tabs");
      if (!wrap) return;
      var idx = tab.dataset.doc;
      wrap.querySelectorAll(".doc-tab").forEach(function (t) { t.classList.toggle("active", t.dataset.doc === idx); });
      wrap.querySelectorAll(".doc-pane").forEach(function (p) { p.classList.toggle("active", p.dataset.doc === idx); });
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    initTheme();
    initTabs();
    initExpand();
    initFilters();
    initLive();
    initDetailLive();
    initDocTabs();
  });
})();
