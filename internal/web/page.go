package web

import (
	"html/template"
	"time"
)

// pageHTML is the project page — one self-contained document (inline styles,
// no external assets). Rendered by pageTmpl in web.go. Kept deliberately plain:
// a legible overview of the repo's stories, tasks, and authored docs.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · project</title>
<style>
  :root {
    --ink: #16181d; --muted: #6b7280; --line: #e5e7eb; --bg: #fbfbfa;
    --accent: #2f6f4f; --chip: #f0f1ef;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--ink);
    font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  }
  .wrap { max-width: 980px; margin: 0 auto; padding: 32px 24px 64px; }
  header { border-bottom: 2px solid var(--ink); padding-bottom: 16px; margin-bottom: 28px; }
  h1 { margin: 0; font-size: 22px; letter-spacing: -0.01em; }
  h1 .dot { color: var(--accent); }
  .meta { color: var(--muted); font-size: 13px; margin-top: 6px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .counts { display: flex; gap: 28px; margin: 20px 0 8px; }
  .count b { font-size: 26px; font-weight: 650; }
  .count span { display: block; color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; }
  section { margin-top: 36px; }
  h2 { font-size: 13px; text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted); margin: 0 0 12px; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 9px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
  th { font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); font-weight: 600; }
  td.id { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; color: var(--muted); white-space: nowrap; }
  td.id a { color: var(--accent); text-decoration: none; }
  a.title { color: inherit; text-decoration: none; }
  a.title:hover, td.id a:hover { text-decoration: underline; }
  .badge { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: 12px; background: var(--chip); color: #374151; }
  .badge.s-open { background: #eef2ff; color: #3730a3; }
  .badge.s-in_progress { background: #fff7ed; color: #9a3412; }
  .badge.s-done { background: #ecfdf5; color: #065f46; }
  .badge.s-blocked { background: #fef2f2; color: #991b1b; }
  .tag { font-size: 11.5px; color: var(--muted); }
  .empty { color: var(--muted); font-style: italic; padding: 8px 10px; }
  .docgrid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 12px; }
  .doc { border: 1px solid var(--line); border-radius: 8px; padding: 12px 14px; background: #fff; }
  .doc .name { font-weight: 600; font-size: 14px; }
  .doc .head { color: var(--muted); font-size: 12.5px; margin-top: 4px; }
  .kind-h { font-size: 12px; color: var(--accent); font-weight: 650; margin: 18px 0 8px; text-transform: lowercase; }
  footer { margin-top: 48px; color: var(--muted); font-size: 12px; border-top: 1px solid var(--line); padding-top: 12px; }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>satelle<span class="dot">.</span> project</h1>
    <div class="meta">{{.RepoRoot}}</div>
    <div class="meta">{{.DBPath}}</div>
    <div class="counts">
      <div class="count"><b>{{len .Stories}}</b><span>stories</span></div>
      <div class="count"><b>{{len .Tasks}}</b><span>tasks</span></div>
    </div>
  </header>

  <section>
    <h2>Stories</h2>
    {{if .Stories}}
    <table>
      <thead><tr><th>ID</th><th>Title</th><th>Status</th><th>Priority</th><th>Tags</th></tr></thead>
      <tbody>
      {{range .Stories}}
        <tr>
          <td class="id"><a href="/story/{{.ID}}">{{.ID}}</a></td>
          <td><a class="title" href="/story/{{.ID}}">{{.Title}}</a></td>
          <td><span class="badge s-{{.Status}}">{{.Status}}</span></td>
          <td>{{if .Priority}}{{.Priority}}{{else}}—{{end}}</td>
          <td class="tag">{{range $i, $t := .Tags}}{{if $i}}, {{end}}{{$t}}{{end}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
    {{else}}<div class="empty">No stories yet — try <code>satelle story create --title "…"</code>.</div>{{end}}
  </section>

  <section>
    <h2>Tasks</h2>
    {{if .Tasks}}
    <table>
      <thead><tr><th>ID</th><th>Title</th><th>Status</th><th>Priority</th></tr></thead>
      <tbody>
      {{range .Tasks}}
        <tr>
          <td class="id"><a href="/task/{{.ID}}">{{.ID}}</a></td>
          <td><a class="title" href="/task/{{.ID}}">{{.Title}}</a></td>
          <td><span class="badge s-{{.Status}}">{{.Status}}</span></td>
          <td>{{if .Priority}}{{.Priority}}{{else}}—{{end}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
    {{else}}<div class="empty">No tasks yet.</div>{{end}}
  </section>

  <section>
    <h2>Authored docs</h2>
    {{range .DocKinds}}
      <div class="kind-h">{{.Kind}}</div>
      {{if .Docs}}
      <div class="docgrid">
        {{range .Docs}}
          <div class="doc">
            <div class="name">{{.Name}}</div>
            {{if .Headline}}<div class="head">{{.Headline}}</div>{{end}}
          </div>
        {{end}}
      </div>
      {{else}}<div class="empty">none indexed — run <code>satelle index</code></div>{{end}}
    {{end}}
  </section>

  <footer>Served locally by satelle · data via the same verbs the CLI uses.</footer>
</div>
</body>
</html>`

// detailFuncs are template helpers for the item detail page.
var detailFuncs = template.FuncMap{
	// ftime renders a timestamp compactly, or "—" when zero.
	"ftime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("2006-01-02 15:04")
	},
}

// detailHTML is the per-item (story/task) page: full fields plus the ledger
// timeline — the URL you share to track one item's progress.
const detailHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · {{.Item.ID}}</title>
<style>
  :root {
    --ink: #16181d; --muted: #6b7280; --line: #e5e7eb; --bg: #fbfbfa;
    --accent: #2f6f4f; --chip: #f0f1ef;
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--ink);
    font: 15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  .wrap { max-width: 820px; margin: 0 auto; padding: 28px 24px 64px; }
  a.back { color: var(--muted); text-decoration: none; font-size: 13px; }
  a.back:hover { text-decoration: underline; }
  header { border-bottom: 2px solid var(--ink); padding: 10px 0 16px; margin-bottom: 22px; }
  .kind { color: var(--accent); font-size: 12px; text-transform: uppercase; letter-spacing: 0.08em; font-weight: 650; }
  h1 { margin: 6px 0 8px; font-size: 21px; letter-spacing: -0.01em; }
  .idline { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; color: var(--muted); }
  .badge { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: 12px; background: var(--chip); color: #374151; }
  .badge.s-open { background: #eef2ff; color: #3730a3; }
  .badge.s-in_progress { background: #fff7ed; color: #9a3412; }
  .badge.s-done { background: #ecfdf5; color: #065f46; }
  .badge.s-blocked { background: #fef2f2; color: #991b1b; }
  .badge.s-cancelled { background: #f3f4f6; color: #6b7280; }
  dl { display: grid; grid-template-columns: 140px 1fr; gap: 6px 16px; margin: 18px 0; }
  dt { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; padding-top: 2px; }
  dd { margin: 0; }
  h2 { font-size: 13px; text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted); margin: 30px 0 12px; }
  pre.prose { white-space: pre-wrap; font: inherit; background: #fff; border: 1px solid var(--line); border-radius: 8px; padding: 12px 14px; margin: 0; }
  .tag { font-size: 12px; color: var(--muted); }
  ol.timeline { list-style: none; margin: 0; padding: 0; border-left: 2px solid var(--line); }
  ol.timeline li { position: relative; padding: 0 0 16px 18px; }
  ol.timeline li::before { content: ""; position: absolute; left: -7px; top: 4px; width: 10px; height: 10px; border-radius: 50%; background: var(--accent); }
  .ev-kind { font-weight: 600; font-size: 13.5px; }
  .ev-meta { color: var(--muted); font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .ev-body { font-size: 14px; margin-top: 2px; }
  .empty { color: var(--muted); font-style: italic; }
</style>
</head>
<body>
<div class="wrap">
  <a class="back" href="/">← project</a>
  <header>
    <div class="kind">{{.Item.Kind}}</div>
    <h1>{{.Item.Title}}</h1>
    <div class="idline">{{.Item.ID}}</div>
  </header>

  <dl>
    <dt>Status</dt><dd><span class="badge s-{{.Item.Status}}">{{.Item.Status}}</span></dd>
    <dt>Priority</dt><dd>{{if .Item.Priority}}{{.Item.Priority}}{{else}}—{{end}}</dd>
    <dt>Category</dt><dd>{{if .Item.Category}}{{.Item.Category}}{{else}}—{{end}}</dd>
    {{if .Item.ParentID}}<dt>Parent</dt><dd class="idline">{{.Item.ParentID}}</dd>{{end}}
    <dt>Tags</dt><dd class="tag">{{if .Item.Tags}}{{range $i, $t := .Item.Tags}}{{if $i}}, {{end}}{{$t}}{{end}}{{else}}—{{end}}</dd>
    <dt>Created</dt><dd>{{ftime .Item.CreatedAt}}</dd>
    <dt>Updated</dt><dd>{{ftime .Item.UpdatedAt}}</dd>
  </dl>

  {{if .Item.Body}}<h2>Description</h2><pre class="prose">{{.Item.Body}}</pre>{{end}}
  {{if .Item.AcceptanceCriteria}}<h2>Acceptance criteria</h2><pre class="prose">{{.Item.AcceptanceCriteria}}</pre>{{end}}

  <h2>Timeline</h2>
  {{if .Events}}
  <ol class="timeline">
    {{range .Events}}
    <li>
      <div class="ev-kind">{{.Kind}}</div>
      <div class="ev-meta">{{ftime .CreatedAt}}{{if .Actor}} · {{.Actor}}{{end}}</div>
      {{if .Body}}<div class="ev-body">{{.Body}}</div>{{end}}
    </li>
    {{end}}
  </ol>
  {{else}}<div class="empty">No ledger events yet.</div>{{end}}

  <footer></footer>
</div>
</body>
</html>`
