package web

import (
	"fmt"
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
	// tagchip renders a tag chip. A key:value tag (e.g. epic:summariser) renders
	// as a kv chip distinguishing key from value; a bare tag renders plain. No
	// schema change — kv is a parsed string convention.
	"tagchip": func(tag string) template.HTML {
		esc := template.HTMLEscapeString
		if i := strings.IndexByte(tag, ':'); i > 0 && i < len(tag)-1 {
			return template.HTML(`<span class="tagchip kv"><span class="k">` + esc(tag[:i]) +
				`</span><span class="v">` + esc(tag[i+1:]) + `</span></span>`)
		}
		return template.HTML(`<span class="tagchip">` + esc(tag) + `</span>`)
	},
	// tabof maps a work-item kind to its panel/tab name (story→stories). Takes
	// any so the workitem.Kind type (a distinct string type) is accepted.
	"tabof": func(kind any) string {
		if fmt.Sprint(kind) == "task" {
			return "tasks"
		}
		return "stories"
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
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="/">project</a> <span class="sep">/</span> <span class="cur" id="crumb-tab">stories</span></nav>
  <header class="app">
    <button class="theme-toggle" id="theme-toggle" type="button" title="Toggle light/dark" aria-label="Toggle light/dark theme">◐</button>
    <h1>satelle<span class="dot">.</span> project<span class="live-dot" title="realtime"></span></h1>
    <div class="meta">{{.RepoRoot}} · <a href="/workspace">workspace →</a> · <a href="/help">help →</a></div>
  </header>

  <div class="tabs" role="tablist">
    <button class="tab" role="tab" data-panel="stories">Stories <span class="n">{{len .Stories}}</span></button>
    <button class="tab" role="tab" data-panel="tasks">Tasks <span class="n">{{len .Tasks}}</span></button>
    <button class="tab" role="tab" data-panel="workflow">Workflow <span class="n">{{len .Workflows}}</span></button>
    <button class="tab" role="tab" data-panel="docs">Documents <span class="n">{{.DocCount}}</span></button>
  </div>

  <section class="panel" data-topic="stories" id="panel-stories">
    <div class="filterbar">
      <input type="text" placeholder="filter… e.g. status:open priority:high order:updated mvp" aria-label="filter stories">
      <div class="chips"></div>
    </div>
    <table class="panel-table">
      <thead><tr><th>ID</th><th>Title</th><th>Status</th><th>Progress</th><th>Priority</th><th>Updated</th></tr></thead>
      <tbody data-rows>{{template "workitemRows" .Stories}}</tbody>
    </table>
  </section>

  <section class="panel" data-topic="tasks" id="panel-tasks">
    <div class="filterbar">
      <input type="text" placeholder="filter… e.g. status:open priority:high order:title" aria-label="filter tasks">
      <div class="chips"></div>
    </div>
    <table class="panel-table">
      <thead><tr><th>ID</th><th>Title</th><th>Status</th><th>Progress</th><th>Priority</th><th>Updated</th></tr></thead>
      <tbody data-rows>{{template "workitemRows" .Tasks}}</tbody>
    </table>
  </section>

  <section class="panel" data-topic="workflow" id="panel-workflow">
    <div class="filterbar">
      <input type="text" placeholder="filter workflows…" aria-label="filter workflows">
      <div class="chips"></div>
    </div>
    <table class="panel-table">
      <thead><tr><th>Name</th><th>Summary</th><th>Applies to</th></tr></thead>
      <tbody data-rows>{{template "workflowRows" .Workflows}}</tbody>
    </table>
  </section>

  <section class="panel" data-topic="docs" id="panel-docs">
    <div class="filterbar">
      <input type="text" placeholder="filter authored docs…" aria-label="filter documents">
      <div class="chips"></div>
    </div>
    <div data-rows>{{template "docsRows" .DocKinds}}</div>
  </section>

  <footer class="site-footer">
    {{if .FooterName}}<span class="footer-name">{{.FooterName}}</span>{{end}}
    {{if .FooterEmail}}<a class="footer-email" href="mailto:{{.FooterEmail}}">{{.FooterEmail}}</a>{{end}}
    <span class="footer-version">satelle {{.Version}}</span>
  </footer>
</div>
<script src="/static/app.js"></script>
</body>
</html>{{end}}

{{define "workitemRows"}}{{range .}}<tr class="row" tabindex="0" role="button" aria-expanded="false" data-status="{{.Status}}" data-priority="{{.Priority}}" data-category="{{.Category}}" data-tags="{{join .Tags ","}}" data-title="{{lower .Title}}" data-updated="{{.UpdatedAt.Format "2006-01-02T15:04:05"}}" data-created="{{.CreatedAt.Format "2006-01-02T15:04:05"}}" data-search="{{printf "%s %s %s" .Title .ID (join .Tags " ") | lower}}" data-expand-url="/fragment/{{.Kind}}/{{.ID}}">
  <td class="id"><span class="id-copy" role="button" tabindex="0" data-id="{{.ID}}" title="Copy id to clipboard">{{.ID}}</span></td>
  <td><div class="wi-title">{{.Title}}</div>{{if .Tags}}<div class="wi-tags">{{range .Tags}}{{tagchip .}}{{end}}</div>{{end}}</td>
  <td><span class="badge s-{{.Status}}">{{.Status}}</span></td>
  <td class="col-reviews">{{range .Lights}}<span class="review-light review-light-{{.State}}" title="{{.Title}}">{{.Index}}</span>{{end}}</td>
  <td>{{if .Priority}}{{.Priority}}{{else}}—{{end}}</td>
  <td class="updated">{{ftime .UpdatedAt}}</td>
</tr>{{else}}<tr><td colspan="6" class="empty">none yet</td></tr>{{end}}{{end}}

{{define "docsRows"}}{{range .}}<div class="kind-h">{{.Kind}}</div>{{if .Docs}}<div class="docgrid">{{range .Docs}}<div class="doc" data-search="{{printf "%s %s" .Name .Headline | lower}}">
  <div class="name">{{.Name}}</div>
  {{if .Headline}}<div class="head">{{.Headline}}</div>{{end}}
  {{if not .ModTime.IsZero}}<div class="updated">updated {{ftime .ModTime}}</div>{{end}}
</div>{{end}}</div>{{else}}<div class="empty">none indexed — run <code>satelle index</code></div>{{end}}{{end}}{{end}}

{{define "workflowRows"}}{{range .}}<tr class="row" tabindex="0" role="button" aria-expanded="false" data-search="{{printf "%s %s %s" .Name .Headline (join .AppliesTo " ") | lower}}" data-expand-url="/fragment/workflow/{{.Name}}">
  <td><div class="wi-title">{{.Name}}</div></td>
  <td>{{.Headline}}</td>
  <td class="wi-tags">{{range .AppliesTo}}{{tagchip (printf "applies_to:%s" .)}}{{end}}</td>
</tr>{{else}}<tr><td colspan="3" class="empty">none indexed — run <code>satelle index</code></td></tr>{{end}}{{end}}

{{define "workflowDetail"}}<div class="expbody">
  <h4>{{.Name}}</h4>
  {{if .Headline}}<div class="meta">{{.Headline}}</div>{{end}}
  {{if .AppliesTo}}<div class="wi-tags">{{range .AppliesTo}}{{tagchip (printf "applies_to:%s" .)}}{{end}}</div>{{end}}

  <h4>States</h4>
  <div class="wf-states">{{range .Spec.States}}<span class="wf-node{{if .Terminal}} terminal{{end}}">{{.Name}}{{if .Actor}}<span class="wf-actor">{{.Actor}}</span>{{end}}</span>{{else}}<span class="empty">no states declared</span>{{end}}</div>

  <h4>Transitions</h4>
  {{if .Spec.Transitions}}<ul class="wf-edges">{{range .Spec.Transitions}}<li class="wf-edge">
    <span class="wf-node sm">{{.From}}</span>
    <span class="wf-arrow">→</span>
    <span class="wf-node sm">{{.To}}</span>
    {{if .Skill}}<span class="wf-gate" title="reviewer gate">{{.Skill}}</span>{{else}}<span class="wf-gate ungated" title="no reviewer skill — advisory">ungated</span>{{end}}
  </li>{{end}}</ul>{{else}}<div class="empty">no transitions declared</div>{{end}}

  <h4>Definition</h4>
  <pre class="prose">{{.Body}}</pre>
</div>{{end}}

{{define "itemDetail"}}<div class="expbody">
  <a class="detail-link open-story" href="/{{.Item.Kind}}/{{.Item.ID}}">Open story →</a>
  <dl>
    <dt>Status</dt><dd><span class="badge s-{{.Item.Status}}">{{.Item.Status}}</span></dd>
    <dt>Priority</dt><dd>{{if .Item.Priority}}{{.Item.Priority}}{{else}}—{{end}}</dd>
    <dt>Category</dt><dd>{{if .Item.Category}}{{.Item.Category}}{{else}}—{{end}}</dd>
    {{if .Item.ParentID}}<dt>Parent</dt><dd><a href="/story/{{.Item.ParentID}}">{{.Item.ParentID}}</a></dd>{{end}}
    {{if .Item.Tags}}<dt>Tags</dt><dd class="wi-tags">{{range .Item.Tags}}{{tagchip .}}{{end}}</dd>{{end}}
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
</div>{{end}}

{{define "workspace"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · workspace</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="/">project</a> <span class="sep">/</span> <span class="cur">workspace</span></nav>
  <header class="app">
    <h1>satelle<span class="dot">.</span> workspace</h1>
    <div class="meta">{{len .Repos}} repos aggregated</div>
  </header>
  {{range .Repos}}<section class="ws-repo">
    <h3 class="kind-h">{{.Name}} <span class="meta">{{.Path}}</span></h3>
    {{if .Err}}<div class="empty">unreadable: {{.Err}}</div>{{else}}
    <div class="meta">{{len .Stories}} stories · {{len .Tasks}} tasks · {{len .Docs}} docs</div>
    {{if .Stories}}<table class="panel-table">
      <thead><tr><th>ID</th><th>Title</th><th>Status</th></tr></thead>
      <tbody>{{range .Stories}}<tr><td class="id">{{.ID}}</td><td>{{.Title}}</td><td><span class="badge s-{{.Status}}">{{.Status}}</span></td></tr>{{end}}</tbody>
    </table>{{end}}
    {{end}}
  </section>{{else}}<div class="empty">no repos registered — run <code>satelle workspace add</code></div>{{end}}
  <footer class="site-footer"><span class="footer-version">satelle workspace</span></footer>
</div>
</body>
</html>{{end}}

{{define "help"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · help</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="/">project</a> <span class="sep">/</span> <span class="cur">help</span></nav>
  <header class="app">
    <h1>satelle<span class="dot">.</span> help</h1>
    <div class="meta">process guides · the same content as <code>satelle help</code></div>
  </header>
  {{range .}}<section class="help-topic" id="{{.Name}}">
    <h2 class="kind-h">{{.Title}} <span class="meta">{{.Name}}</span></h2>
    <pre class="prose">{{.Body}}</pre>
  </section>{{else}}<div class="empty">no help topics</div>{{end}}
  <footer class="site-footer"><span class="footer-version">satelle help</span></footer>
</div>
</body>
</html>{{end}}

{{define "detailPage"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · {{.Item.ID}}</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="/">project</a> <span class="sep">/</span> <a href="/#{{tabof .Item.Kind}}">{{.Item.Kind}}</a> <span class="sep">/</span> <span class="cur">{{.Item.ID}}</span></nav>
  <header class="app">
    <div class="kind-h">{{.Item.Kind}}<span class="live-dot" title="realtime"></span></div>
    <h1>{{.Item.Title}}</h1>
    <div class="meta">{{.Item.ID}}</div>
  </header>
  <div id="detail-live" data-kind="{{.Item.Kind}}" data-id="{{.Item.ID}}">{{template "itemDetail" .}}</div>
</div>
<script src="/static/app.js"></script>
</body>
</html>{{end}}
`
