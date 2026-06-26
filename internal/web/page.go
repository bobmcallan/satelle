package web

import (
	"html/template"
	"strings"
	"time"
)

// tmplFuncs are shared template helpers.
var tmplFuncs = template.FuncMap{
	"ftime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("2006-01-02 15:04")
	},
	"lower": strings.ToLower,
	"join": func(ss []string, sep string) string {
		return strings.Join(ss, sep)
	},
}

// tmpl is the whole page's template set: the full page, the per-panel row
// fragments (reused by the realtime refetch), and the inline item detail
// (reused by the expand fragment and the standalone detail page).
var tmpl = template.Must(template.New("web").Funcs(tmplFuncs).Parse(templatesSrc))

const templatesSrc = `
{{define "page"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · project</title>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="wrap">
  <header class="app">
    <h1>satelle<span class="dot">.</span> project<span class="live-dot" title="realtime"></span></h1>
    <div class="meta">{{.RepoRoot}}</div>
  </header>

  <div class="tabs" role="tablist">
    <button class="tab" role="tab" data-panel="stories">Stories <span class="n">{{len .Stories}}</span></button>
    <button class="tab" role="tab" data-panel="tasks">Tasks <span class="n">{{len .Tasks}}</span></button>
    <button class="tab" role="tab" data-panel="docs">Documents <span class="n">{{.DocCount}}</span></button>
  </div>

  <section class="panel" data-topic="stories" id="panel-stories">
    <div class="filterbar">
      <input type="text" placeholder="filter… e.g. status:open priority:high mvp" aria-label="filter stories">
      <div class="chips"></div>
    </div>
    <table class="panel-table">
      <thead><tr><th>ID</th><th>Title</th><th>Status</th><th>Priority</th><th>Tags</th></tr></thead>
      <tbody data-rows>{{template "workitemRows" .Stories}}</tbody>
    </table>
  </section>

  <section class="panel" data-topic="tasks" id="panel-tasks">
    <div class="filterbar">
      <input type="text" placeholder="filter… e.g. status:open priority:high" aria-label="filter tasks">
      <div class="chips"></div>
    </div>
    <table class="panel-table">
      <thead><tr><th>ID</th><th>Title</th><th>Status</th><th>Priority</th><th>Tags</th></tr></thead>
      <tbody data-rows>{{template "workitemRows" .Tasks}}</tbody>
    </table>
  </section>

  <section class="panel" data-topic="docs" id="panel-docs">
    <div class="filterbar">
      <input type="text" placeholder="filter authored docs…" aria-label="filter documents">
      <div class="chips"></div>
    </div>
    <div data-rows>{{template "docsRows" .DocKinds}}</div>
  </section>

  <footer class="app">Served locally by satelle · live updates over the same verbs the CLI uses.</footer>
</div>
<script src="/static/app.js"></script>
</body>
</html>{{end}}

{{define "workitemRows"}}{{range .}}<tr class="row" tabindex="0" role="button" aria-expanded="false" data-status="{{.Status}}" data-priority="{{.Priority}}" data-category="{{.Category}}" data-tags="{{join .Tags ","}}" data-search="{{printf "%s %s %s" .Title .ID (join .Tags " ") | lower}}" data-expand-url="/fragment/{{.Kind}}/{{.ID}}">
  <td class="id"><span class="caret"></span><a href="/{{.Kind}}/{{.ID}}">{{.ID}}</a></td>
  <td>{{.Title}}</td>
  <td><span class="badge s-{{.Status}}">{{.Status}}</span></td>
  <td>{{if .Priority}}{{.Priority}}{{else}}—{{end}}</td>
  <td class="tag">{{range $i, $t := .Tags}}{{if $i}}, {{end}}{{$t}}{{end}}</td>
</tr>{{else}}<tr><td colspan="5" class="empty">none yet</td></tr>{{end}}{{end}}

{{define "docsRows"}}{{range .}}<div class="kind-h">{{.Kind}}</div>{{if .Docs}}<div class="docgrid">{{range .Docs}}<div class="doc" data-search="{{printf "%s %s" .Name .Headline | lower}}">
  <div class="name">{{.Name}}</div>
  {{if .Headline}}<div class="head">{{.Headline}}</div>{{end}}
</div>{{end}}</div>{{else}}<div class="empty">none indexed — run <code>satelle index</code></div>{{end}}{{end}}{{end}}

{{define "itemDetail"}}<div class="expbody">
  <dl>
    <dt>Status</dt><dd><span class="badge s-{{.Item.Status}}">{{.Item.Status}}</span></dd>
    <dt>Priority</dt><dd>{{if .Item.Priority}}{{.Item.Priority}}{{else}}—{{end}}</dd>
    <dt>Category</dt><dd>{{if .Item.Category}}{{.Item.Category}}{{else}}—{{end}}</dd>
    {{if .Item.ParentID}}<dt>Parent</dt><dd><a href="/story/{{.Item.ParentID}}">{{.Item.ParentID}}</a></dd>{{end}}
    <dt>Tags</dt><dd class="tag">{{if .Item.Tags}}{{range $i, $t := .Item.Tags}}{{if $i}}, {{end}}{{$t}}{{end}}{{else}}—{{end}}</dd>
    <dt>Updated</dt><dd>{{ftime .Item.UpdatedAt}}</dd>
  </dl>
  {{if .Item.Body}}<h4>Description</h4><pre class="prose">{{.Item.Body}}</pre>{{end}}
  {{if .Item.AcceptanceCriteria}}<h4>Acceptance criteria</h4><pre class="prose">{{.Item.AcceptanceCriteria}}</pre>{{end}}
  <h4>Timeline</h4>
  {{if .Events}}<ol class="timeline">{{range .Events}}<li>
    <div class="ev-kind">{{.Kind}}</div>
    <div class="ev-meta">{{ftime .CreatedAt}}{{if .Actor}} · {{.Actor}}{{end}}</div>
    {{if .Body}}<div class="ev-body">{{.Body}}</div>{{end}}
  </li>{{end}}</ol>{{else}}<div class="empty">No ledger events yet.</div>{{end}}
  <p><a class="detail-link" href="/{{.Item.Kind}}/{{.Item.ID}}">open full page →</a></p>
</div>{{end}}

{{define "detailPage"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · {{.Item.ID}}</title>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="wrap">
  <p><a href="/">← project</a></p>
  <header class="app">
    <div class="kind-h">{{.Item.Kind}}</div>
    <h1>{{.Item.Title}}</h1>
    <div class="meta">{{.Item.ID}}</div>
  </header>
  {{template "itemDetail" .}}
</div>
</body>
</html>{{end}}
`
