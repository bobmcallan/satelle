package web

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/buildinfo"
)

// basePath is the URL prefix this server is mounted under, empty for the
// supervisor (which serves the / landing and shared chrome) and "/<slug>" for a
// project served behind the supervisor's reverse proxy. It is a process global
// because each project is its own process.
var basePath string

// SetBasePath sets the mount prefix (trailing slash trimmed). Call before New.
func SetBasePath(p string) {
	basePath = "/" + strings.Trim(p, "/")
	if basePath == "/" {
		basePath = ""
	}
}

// baseHref returns the value for the page's <base href> — always slash-terminated
// so relative URLs in app.js resolve under the mount: "/" at root, "/slug/" under
// the proxy.
func baseHref() string {
	if basePath == "" {
		return "/"
	}
	return basePath + "/"
}

// tmplFuncs are shared template helpers.
var tmplFuncs = template.FuncMap{
	"basehref": baseHref,
	// version / footeremail back the one shared site footer (see the "footer"
	// template) so it needs no per-page data: version is baked into the binary,
	// the operator email is resolved once from git identity at server start.
	"version":     func() string { return buildinfo.Resolve().Version },
	"footeremail": func() string { return footerEmail },
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
	//
	// A chip that maps to a real filter facet renders as a <button> carrying the
	// exact filter token it adds when clicked (app.js wires it to the panel's
	// filter input): a category:<v> chip filters the category facet; every other
	// tag filters the tags facet (tags:<full-tag>). scope:/applies_to: chips are
	// workflow metadata, not filter facets, so they stay inert spans.
	"tagchip": func(tag string) template.HTML {
		esc := template.HTMLEscapeString
		cls := "tagchip"
		inner := esc(tag)
		key := ""
		if i := strings.IndexByte(tag, ':'); i > 0 && i < len(tag)-1 {
			key = tag[:i]
			cls = "tagchip kv"
			if key == "category" { // category gets a distinct key colour, like satellites
				cls += " cat"
			}
			inner = `<span class="k">` + esc(key) + `</span><span class="v">` + esc(tag[i+1:]) + `</span>`
		}
		token := ""
		switch key {
		case "scope", "applies_to":
			// inert: workflow metadata, not a filter facet
		case "category":
			token = tag // category:<value> is itself the filter token
		default:
			token = "tags:" + tag
		}
		if token == "" {
			return template.HTML(`<span class="` + cls + `">` + inner + `</span>`)
		}
		return template.HTML(`<button type="button" class="` + cls + ` clickable" data-filter="` +
			esc(token) + `" aria-label="filter by ` + esc(tag) + `">` + inner + `</button>`)
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
{{define "topbar"}}<button class="theme-toggle" id="theme-toggle" type="button" title="Toggle light/dark" aria-label="Toggle light/dark theme">◐</button>{{if .Uptime}}<button class="uptime" type="button" disabled title="web service uptime — green border means up">{{.Uptime}}</button>{{end}}{{end}}

{{define "footer"}}<footer class="site-footer">{{if footeremail}}<a class="footer-email" href="mailto:{{footeremail}}">{{footeremail}}</a>{{end}}<span class="footer-version">satelle {{version}}</span></footer>{{end}}

{{define "page"}}<!doctype html>
<html lang="en"{{if .Theme}} data-theme="{{.Theme}}"{{end}}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · project</title>
<script>(function(){try{if(!document.documentElement.getAttribute('data-theme')){var t=localStorage.getItem('satelle-theme');if(t==='dark')document.documentElement.setAttribute('data-theme','dark');}}catch(e){}})();</script>
<base href="{{basehref}}">
<link rel="stylesheet" href="static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="{{basehref}}">project</a> <span class="sep">/</span> <span class="cur" id="crumb-tab">stories</span></nav>
  <header class="app">
    {{template "topbar" .TopBar}}
    <h1>satelle<span class="dot">.</span> project</h1>
    <div class="meta">{{.RepoRoot}} · <a href="projects">projects →</a> · <a href="workspace">workspace →</a> · <a href="help">help →</a></div>
  </header>

  <div class="tabs" role="tablist">
    <button class="tab" role="tab" data-panel="stories">Stories <span class="n">{{len .Stories}}</span>{{if .BacklogCount}} <span class="n-backlog" title="stories in the open backlog">{{.BacklogCount}} backlog</span>{{end}}</button>
    <button class="tab" role="tab" data-panel="tasks">Tasks <span class="n">{{len .Tasks}}</span></button>
    <button class="tab" role="tab" data-panel="workflow">Workflow <span class="n">{{len .Workflows}}</span></button>
    <button class="tab" role="tab" data-panel="docs">Documents <span class="n">{{.DocCount}}</span></button>
  </div>

  <section class="panel" data-topic="stories" id="panel-stories">
    <div class="filterbar">
      <div class="filter-input">
        <input type="text" placeholder="filter… e.g. status:open priority:high tags:epic:foo order:updated" aria-label="filter stories">
        <span class="filter-count" aria-live="polite"></span>
      </div>
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
      <thead><tr><th>Name</th><th>Summary</th><th>Updated</th></tr></thead>
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

  {{template "footer"}}
</div>
<script src="static/app.js"></script>
</body>
</html>{{end}}

{{define "workitemRows"}}{{range .}}<tr class="row" tabindex="0" role="button" aria-expanded="false" data-status="{{.Status}}" data-priority="{{.Priority}}" data-category="{{.Category}}" data-tags="{{join .Tags ","}}" data-title="{{lower .Title}}" data-updated="{{.UpdatedAt.Format "2006-01-02T15:04:05"}}" data-created="{{.CreatedAt.Format "2006-01-02T15:04:05"}}" data-search="{{printf "%s %s %s" .Title .ID (join .Tags " ") | lower}}" data-expand-url="fragment/{{.Kind}}/{{.ID}}">
  <td class="id"><span class="id-copy" role="button" tabindex="0" data-id="{{.ID}}" title="Copy id to clipboard">{{.ID}}</span></td>
  <td><div class="wi-title">{{.Title}}</div>{{if or .Category .Tags}}<div class="wi-tags">{{if .Category}}{{tagchip (printf "category:%s" .Category)}}{{end}}{{range .Tags}}{{tagchip .}}{{end}}</div>{{end}}</td>
  <td><span class="badge s-{{.Status}}">{{.Status}}</span></td>
  <td class="col-reviews">{{range .Lights}}<span class="review-light review-light-{{.State}}" title="{{.Title}}">{{.Index}}</span>{{end}}</td>
  <td>{{if .Priority}}{{.Priority}}{{else}}—{{end}}</td>
  <td class="updated">{{ftime .UpdatedAt}}</td>
</tr>{{else}}<tr><td colspan="6" class="empty">none yet</td></tr>{{end}}{{end}}

{{define "docsRows"}}{{range .}}{{$k := .Kind}}<div class="kind-h">{{.Kind}}</div>{{if .Docs}}<div class="docgrid">{{range .Docs}}<a class="doc" href="doc/{{$k}}/{{.Name}}" data-search="{{printf "%s %s" .Name .Headline | lower}}">
  <div class="name">{{.Name}}</div>
  {{if .Headline}}<div class="head">{{.Headline}}</div>{{end}}
  {{if not .ModTime.IsZero}}<div class="updated">updated {{ftime .ModTime}}</div>{{end}}
</a>{{end}}</div>{{else}}<div class="empty">none indexed — run <code>satelle index</code></div>{{end}}{{end}}{{end}}

{{define "workflowRows"}}{{range .}}<tr class="row" tabindex="0" role="button" aria-expanded="false" data-search="{{printf "%s %s %s %s" .Name .Headline .Scope (join .AppliesTo " ") | lower}}" data-expand-url="fragment/workflow/{{.Name}}">
  <td><div class="wi-title">{{.Name}}</div><div class="wi-tags">{{if .Scope}}{{tagchip (printf "scope:%s" .Scope)}}{{end}}{{range .AppliesTo}}{{tagchip (printf "applies_to:%s" .)}}{{end}}</div></td>
  <td>{{.Headline}}</td>
  <td class="updated">{{ftime .Updated}}</td>
</tr>{{else}}<tr><td colspan="3" class="empty">none indexed — run <code>satelle index</code></td></tr>{{end}}{{end}}

{{define "workflowDetail"}}<div class="expbody">
  <h4>{{.Name}}</h4>
  {{if .Headline}}<div class="meta">{{.Headline}}</div>{{end}}
  <div class="wi-tags">{{if .Scope}}{{tagchip (printf "scope:%s" .Scope)}}{{end}}{{range .AppliesTo}}{{tagchip (printf "applies_to:%s" .)}}{{end}}</div>

  {{if .Diagram}}<h4>Flow</h4>
  <div class="wf-diagram-wrap">{{.Diagram}}</div>{{end}}

  <h4>States</h4>
  <div class="wf-states">{{range .Spec.States}}<span class="wf-node{{if .Terminal}} terminal{{end}}">{{.Name}}{{if .Agent}}<span class="wf-agent">{{.Agent}}</span>{{end}}</span>{{else}}<span class="empty">no states declared</span>{{end}}</div>

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
  {{if not .Standalone}}<a class="detail-link open-story" href="{{.Item.Kind}}/{{.Item.ID}}">Open story →</a>{{end}}
  {{if .Docs}}<div class="doc-tabs">
    <div class="doc-tabstrip" role="tablist">{{range $i, $d := .Docs}}<button class="doc-tab{{if eq $i 0}} active{{end}}" type="button" role="tab" data-doc="{{$i}}">{{$d.Name}}{{if $d.Type}} <span class="doc-tab-type">{{$d.Type}}</span>{{end}}</button>{{end}}</div>
    {{range $i, $d := .Docs}}<div class="doc-pane{{if eq $i 0}} active{{end}}" data-doc="{{$i}}"><article class="doc-article">{{$d.HTML}}</article></div>{{end}}
  </div>{{end}}
  <dl>
    <dt>Status</dt><dd><span class="badge s-{{.Item.Status}}">{{.Item.Status}}</span></dd>
    <dt>Priority</dt><dd>{{if .Item.Priority}}{{.Item.Priority}}{{else}}—{{end}}</dd>
    <dt>Category</dt><dd>{{if .Item.Category}}{{.Item.Category}}{{else}}—{{end}}</dd>
    {{if .Item.ParentID}}<dt>Parent</dt><dd><a href="story/{{.Item.ParentID}}">{{.Item.ParentID}}</a></dd>{{end}}
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
<base href="{{basehref}}">
<link rel="stylesheet" href="static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="{{basehref}}">project</a> <span class="sep">/</span> <span class="cur">workspace</span></nav>
  <header class="app">
    {{template "topbar" .TopBar}}
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
  {{template "footer"}}
</div>
</body>
</html>{{end}}

{{define "projects"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · projects</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<base href="{{basehref}}">
<link rel="stylesheet" href="static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><span class="cur">projects</span></nav>
  <header class="app">
    {{template "topbar" .TopBar}}
    <h1>satelle<span class="dot">.</span> projects</h1>
    <div class="meta">{{len .Projects}} connected project{{if ne (len .Projects) 1}}s{{end}} · <a href="help">help →</a></div>
  </header>
  {{range .Projects}}<a class="proj-card" href="{{.URL}}">
    <div class="proj-name">{{.Name}} <span class="proj-slug">/{{.Slug}}/</span></div>
    <div class="meta">{{.Path}}</div>
    <div class="meta">{{.Stories}} stories · {{.Tasks}} tasks · {{.Docs}} docs</div>
  </a>{{else}}<div class="empty">no projects registered — run <code>satelle workspace add</code></div>{{end}}
  <article class="doc-article landing-help">
    <h2>Add a project</h2>
    <p>Register any repo and it appears here within a few seconds, served at <code>/&lt;slug&gt;/</code> — live, no restart:</p>
    <pre><code>satelle workspace add /path/to/repo</code></pre>
    <p>Stop serving one with <code>satelle workspace remove &lt;path&gt;</code>; list them with <code>satelle workspace list</code>.</p>
    <h2>Help &amp; updates</h2>
    <p><a href="help">Process guides →</a> · keep the binary current with <code>satelle update</code> (<code>--check</code> to peek first).</p>
  </article>
  {{template "footer"}}
</div>
<script src="static/app.js"></script>
</body>
</html>{{end}}

{{define "help"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · help</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<base href="{{basehref}}">
<link rel="stylesheet" href="static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="{{basehref}}">project</a> <span class="sep">/</span> <span class="cur">help</span></nav>
  <header class="app">
    {{template "topbar" .TopBar}}
    <h1>satelle<span class="dot">.</span> help</h1>
    <div class="meta">process guides · the same content as <code>satelle help</code></div>
  </header>
  {{range .Topics}}<section class="help-topic" id="{{.Name}}">
    <h2 class="kind-h">{{.Title}} <span class="meta">{{.Name}}</span></h2>
    <pre class="prose">{{.Body}}</pre>
  </section>{{else}}<div class="empty">no help topics</div>{{end}}
  {{template "footer"}}
</div>
</body>
</html>{{end}}

{{define "docPage"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · {{.Name}}</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<base href="{{basehref}}">
<link rel="stylesheet" href="static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="{{basehref}}">project</a> <span class="sep">/</span> <a href="{{basehref}}#docs">docs</a> <span class="sep">/</span> <span class="cur">{{.Name}}</span></nav>
  <header class="app">
    {{template "topbar" .TopBar}}
    <div class="kind-h">{{.Kind}}</div>
    <h1>{{.Name}}</h1>
    {{if .Headline}}<div class="meta">{{.Headline}}</div>{{end}}
  </header>
  <article class="doc-article">{{.HTML}}</article>
  {{template "footer"}}
</div>
<script src="static/app.js"></script>
</body>
</html>{{end}}

{{define "detailPage"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>satelle · {{.Item.ID}}</title>
<script>(function(){try{var t=localStorage.getItem('satelle-theme');if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>
<base href="{{basehref}}">
<link rel="stylesheet" href="static/app.css">
</head>
<body>
<div class="wrap">
  <nav class="crumbs"><a href="{{basehref}}">project</a> <span class="sep">/</span> <a href="{{basehref}}#{{tabof .Item.Kind}}">{{.Item.Kind}}</a> <span class="sep">/</span> <span class="cur">{{.Item.ID}}</span></nav>
  <header class="app">
    {{template "topbar" .TopBar}}
    <div class="kind-h">{{.Item.Kind}}</div>
    <h1>{{.Item.Title}}</h1>
    <div class="meta">{{.Item.ID}}</div>
  </header>
  <div id="detail-live" data-kind="{{.Item.Kind}}" data-id="{{.Item.ID}}">{{template "itemDetail" .}}</div>
  {{template "footer"}}
</div>
<script src="static/app.js"></script>
</body>
</html>{{end}}
`
