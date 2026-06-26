// app.js — satelle project page interactivity (vanilla, no framework).
//
//  * Tabs        — show one panel at a time; active tab in the URL hash.
//  * Realtime    — /events SSE doorbell; on a topic trigger, refetch that
//                  panel's rows fragment and swap it in, then re-apply filters.
//  * Expand      — click a row to fetch + reveal its detail + ledger timeline
//                  inline; click again to collapse.
//  * Filter      — a query box parsed into removable chips (status:/priority:/
//                  category:/tags: + free text); status:open hides terminal rows.
(function () {
  "use strict";
  var TERMINAL = { done: 1, cancelled: 1 };
  var PANELS = ["stories", "tasks", "docs"];

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
    document.querySelectorAll(".tab").forEach(function (t) {
      t.addEventListener("click", function () {
        var name = t.dataset.panel;
        history.replaceState(null, "", "#" + name);
        showTab(name);
      });
    });
    showTab((location.hash || "#stories").slice(1));
  }

  // ---- filtering -----------------------------------------------------------
  var FILTER_KEYS = { status: 1, priority: 1, category: 1, tags: 1 };

  function parseQuery(q) {
    var tokens = [], free = [];
    (q || "").trim().split(/\s+/).forEach(function (part) {
      if (!part) return;
      var i = part.indexOf(":");
      if (i > 0 && FILTER_KEYS[part.slice(0, i).toLowerCase()]) {
        tokens.push({ key: part.slice(0, i).toLowerCase(), vals: part.slice(i + 1).toLowerCase().split(",").filter(Boolean) });
      } else {
        free.push(part.toLowerCase());
      }
    });
    return { tokens: tokens, free: free };
  }

  function rowMatches(row, parsed) {
    var hasStatus = false;
    for (var k = 0; k < parsed.tokens.length; k++) {
      var t = parsed.tokens[k];
      if (t.key === "status") {
        hasStatus = true;
        if (t.vals.indexOf("all") === -1) {
          var st = row.dataset.status || "";
          if (t.vals.indexOf(st) === -1) return false;
        }
      } else if (t.key === "tags") {
        var tags = (row.dataset.tags || "").toLowerCase().split(",");
        if (!t.vals.some(function (v) { return tags.indexOf(v) !== -1; })) return false;
      } else {
        var val = (row.dataset[t.key] || "").toLowerCase();
        if (t.vals.indexOf("all") === -1 && t.vals.indexOf(val) === -1) return false;
      }
    }
    // Default: hide terminal rows unless a status token was given.
    if (!hasStatus && TERMINAL[row.dataset.status]) return false;
    var search = (row.dataset.search || "").toLowerCase();
    return parsed.free.every(function (term) { return search.indexOf(term) !== -1; });
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
      b.addEventListener("click", function () { onRemove(); });
      c.appendChild(b);
      box.appendChild(c);
    }
    parsed.tokens.forEach(function (t) {
      chip(t.key + ":" + t.vals.join(","), false, function () {
        input.value = rebuild(parsed, t, null);
        applyFilter(panel);
      });
    });
    parsed.free.forEach(function (term) {
      chip(term, false, function () {
        input.value = rebuild(parsed, null, term);
        applyFilter(panel);
      });
    });
    var hasStatus = parsed.tokens.some(function (t) { return t.key === "status"; });
    var isDocs = panel.dataset.topic === "docs";
    if (!hasStatus && !isDocs) {
      chip("status:open", true, function () {
        input.value = (input.value.trim() + " status:all").trim();
        applyFilter(panel);
      });
    }
  }

  // rebuild the query string from parsed tokens, dropping one token or free term.
  function rebuild(parsed, dropTok, dropFree) {
    var parts = [];
    parsed.tokens.forEach(function (t) {
      if (t === dropTok) return;
      parts.push(t.key + ":" + t.vals.join(","));
    });
    parsed.free.forEach(function (f) {
      if (f === dropFree) return;
      parts.push(f);
    });
    return parts.join(" ");
  }

  function applyFilter(panel) {
    var input = panel.querySelector(".filterbar input");
    var parsed = parseQuery(input ? input.value : "");
    collapseAll(panel);
    panel.querySelectorAll("[data-rows] .row, [data-rows] .doc").forEach(function (row) {
      row.style.display = rowMatches(row, parsed) ? "" : "none";
    });
    if (input) renderChips(panel, parsed, input);
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
    panel.querySelectorAll("tr.row[aria-expanded='true']").forEach(function (row) {
      row.setAttribute("aria-expanded", "false");
      var next = row.nextElementSibling;
      if (next && next.classList.contains("expansion")) next.remove();
    });
  }

  function toggleRow(row) {
    var open = row.getAttribute("aria-expanded") === "true";
    var next = row.nextElementSibling;
    if (open) {
      row.setAttribute("aria-expanded", "false");
      if (next && next.classList.contains("expansion")) next.remove();
      return;
    }
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

  function initExpand() {
    document.querySelectorAll(".panel").forEach(function (panel) {
      panel.addEventListener("click", function (e) {
        if (e.target.closest("a")) return; // let real links through
        var row = e.target.closest("tr.row[data-expand-url]");
        if (row) toggleRow(row);
      });
      panel.addEventListener("keydown", function (e) {
        if (e.key !== "Enter" && e.key !== " ") return;
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
    fetch("/fragment/" + topic)
      .then(function (r) { return r.text(); })
      .then(function (html) {
        holder.innerHTML = html;
        applyFilter(panel);
        var tab = document.querySelector('.tab[data-panel="' + topic + '"] .n');
        if (tab) {
          var n = panel.querySelectorAll("[data-rows] .row, [data-rows] .doc").length;
          tab.textContent = n;
        }
      })
      .catch(function () {});
  }

  function initLive() {
    if (!window.EventSource) return;
    var dot = document.querySelector(".live-dot");
    var src = new EventSource("/events");
    src.addEventListener("open", function () { if (dot) dot.classList.add("on"); });
    src.addEventListener("trigger", function (ev) { refetchPanel(ev.data); });
    src.onerror = function () { if (dot) dot.classList.remove("on"); };
  }

  document.addEventListener("DOMContentLoaded", function () {
    initTabs();
    initExpand();
    initFilters();
    initLive();
  });
})();
