// Package remotedef reads and validates a remote definition: a self-contained
// directory (.satelle/config/remotes/<name>/) naming a pinned satelle version,
// the commands that reach a host, the identity label that host runs under, the
// lifecycle segments it serves, its triggers, the .env key names its agents may
// read — and its OWN workflows, skills, principles and agent bindings.
//
// The definition is data. This package runs nothing and opens nothing outside
// the directory it is handed: it never reads the repo's .satelle/workflows, so a
// remote's verdict cannot depend on any developer's local process. It holds no
// credential and names no host, vendor or provider — host argv, the identity
// label and the trigger kind are opaque strings the definition's author owns.
package remotedef

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/semver"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// File names inside a remote directory, relative to it.
const (
	DefinitionFile = "remote.toml"
	workflowsDir   = "workflows"
	skillsDir      = "skills"
	principlesDir  = "principles"
	doneFile       = workflowsDir + "/done.toml"
	stepFile       = workflowsDir + "/step.toml"
	agentsFile     = workflowsDir + "/" + config.AgentsConfigName
)

// Definition is the decoded remote.toml.
type Definition struct {
	// SatelleVersion pins the satelle the remote runs (MAJOR.MINOR.PATCH).
	SatelleVersion string `toml:"satelle_version"`
	// Secrets are the .satelle/config/.env key names the remote's agents may
	// read. Names only — a value never appears in a definition.
	Secrets []string `toml:"secrets"`
	// WakeKey is the .env key name of the wake-only credential used to start the
	// node.
	WakeKey string `toml:"wake_key"`
	// Lifecycle is what happens to the node when a run ends: "shutdown" (the
	// default when absent) stops it with host.stop, "idle" leaves it running.
	Lifecycle string `toml:"lifecycle"`
	// Repos are fetched onto the node read-only, each at its ref.
	Repos    []Repo    `toml:"repo"`
	Network  Network   `toml:"network"`
	Identity Identity  `toml:"identity"`
	Host     Host      `toml:"host"`
	Segments []Segment `toml:"segment"`
	Triggers []Trigger `toml:"trigger"`
	// Preflight is what is checked on the host before the remote starts, beyond
	// the per-step probes.
	Preflight Preflight `toml:"preflight"`
	// Steps declare, per status of the remote's own workflow, the probes the
	// preflight runs and the legs the step holds.
	Steps []Step `toml:"step"`
}

// Expectations a probe carries: the outcome the preflight compares its run with.
const (
	// ExpectSucceed is a probe the host identity must be able to run.
	ExpectSucceed = "succeed"
	// ExpectRefused is a probe the host identity must be refused.
	ExpectRefused = "refused"
)

// Legs a step may hold. A step holding all three can read private data, take in
// untrusted input and send it out, which the preflight refuses.
const (
	LegData      = "data"
	LegUntrusted = "untrusted"
	LegOutbound  = "outbound"
)

var allLegs = []string{LegData, LegUntrusted, LegOutbound}

// Preflight is the host-wide part of the preflight. Every argv is opaque text the
// author owns; satelle runs it on the host and reads its stdout.
type Preflight struct {
	// EnvProbe prints the host's environment as KEY=VALUE lines.
	EnvProbe []string `toml:"env_probe"`
	// FileProbe prints one path per line for each credential file on the host.
	FileProbe []string `toml:"file_probe"`
	// AllowCredentials are the env key names and file paths the author accepts
	// finding; anything else credential-shaped fails the preflight.
	AllowCredentials []string `toml:"allow_credentials"`
}

// Step is one status of the remote's own workflow with what the preflight
// checks for it.
type Step struct {
	Name   string   `toml:"name"`
	Legs   []string `toml:"legs"`
	Probes []Probe  `toml:"probe"`
}

// Probe is one command the preflight runs on the host and the outcome the
// definition expects of it: ExpectSucceed or ExpectRefused.
type Probe struct {
	Argv   []string `toml:"argv"`
	Expect string   `toml:"expect"`
}

// The two lifecycles a definition may name.
const (
	LifecycleShutdown = "shutdown"
	LifecycleIdle     = "idle"
)

// EffectiveLifecycle is the lifecycle in force: the one named, else shutdown.
func (d Definition) EffectiveLifecycle() string {
	if d.Lifecycle == "" {
		return LifecycleShutdown
	}
	return d.Lifecycle
}

// Repo is one repository the node fetches, read-only: an https URL and the ref
// (branch, tag or commit) to check out.
type Repo struct {
	URL string `toml:"url"`
	Ref string `toml:"ref"`
}

// Network is the node's egress beyond what the definition already implies.
type Network struct {
	// Allow are extra hostnames (or *.domain) the node may reach.
	Allow []string `toml:"allow"`
}

// Identity is the host identity the remote runs under: a label the platform
// understands. satelle treats it as an opaque string.
type Identity struct {
	Label string `toml:"label"`
}

// Host is the user's own tooling for reaching the host, each an argv list. The
// placeholders {label}, {command} and {allow} are filled by whatever runs it.
type Host struct {
	// Name is the host's name as the team knows it, recorded verbatim on each
	// remote_run row. Optional and opaque: satelle never derives one.
	Name  string   `toml:"name"`
	Start []string `toml:"start"`
	// Network applies the egress allowlist to the host: {allow} in it is the
	// comma-joined list of hosts (see Allowlist).
	Network []string `toml:"network"`
	Run     []string `toml:"run"`
	Stop    []string `toml:"stop"`
	// StoryCommand is the one command that works a single story on the host, with
	// StoryPlaceholder standing for the story's id. It is a string, not an argv:
	// it is the value the run command's {command} placeholder takes. Optional;
	// when present it must carry the placeholder.
	StoryCommand string `toml:"story_command"`
	// Bootstrap are commands run on the node, in order, after the pinned satelle
	// is installed and before it joins. Each is an argv list.
	Bootstrap [][]string `toml:"bootstrap"`
	// Env is the environment the host commands run under. A credential-shaped
	// name must hold a ${VAR} reference, never a value.
	Env map[string]string `toml:"env"`
}

// Segment is one stretch of the lifecycle the remote serves: an entry status and
// an exit status, both statuses of the remote's own workflow. Categories narrows
// the segment to stories of those categories (each a category of the remote's own
// workflow, with entry and exit on that category's route); none means every
// category.
type Segment struct {
	Entry      string   `toml:"entry"`
	Exit       string   `toml:"exit"`
	Categories []string `toml:"categories"`
}

// Trigger is one way the remote is started. Name is what the server's declared
// triggers and a wake are keyed by; Kind is opaque to satelle.
type Trigger struct {
	Name   string            `toml:"name"`
	Kind   string            `toml:"kind"`
	Params map[string]string `toml:"params"`
}

// envKeyPattern is the shape of a .env key name.
var envKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// namePattern is the shape of a remote's directory name.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidName reports whether name can be a remote's directory name.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Dir is where the named remote lives under a data dir.
func Dir(dataDir, name string) string {
	return filepath.Join(dataDir, filepath.FromSlash(config.RemotesDir), name)
}

// Names lists the remotes defined under a data dir, sorted. A data dir with no
// remotes directory has none — absent is not an error.
func Names(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dataDir, filepath.FromSlash(config.RemotesDir)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Load reads dir's remote.toml. It refuses a file with keys the definition does
// not know, so a misspelt key is never silently ignored. Validate is the check
// that reports every defect; Load is for a caller that needs the values.
func Load(dir string) (Definition, error) {
	body, err := os.ReadFile(filepath.Join(dir, DefinitionFile))
	if err != nil {
		return Definition{}, err
	}
	def, md, err := decode(string(body))
	if err != nil {
		return Definition{}, fmt.Errorf("%s: %w", DefinitionFile, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return Definition{}, fmt.Errorf("%s: unknown key %s", DefinitionFile, un[0])
	}
	return def, nil
}

func decode(body string) (Definition, toml.MetaData, error) {
	var def Definition
	md, err := toml.Decode(body, &def)
	return def, md, err
}

// Validate checks the remote in dir and returns every error it finds, one
// finding each. Every check runs on its own, so a defect in one file never
// hides a defect in another. Each finding names its file relative to dir and
// the key at fault; no finding carries a value.
func Validate(dir string) []health.Finding {
	v := &validation{dir: dir}
	v.checkDefinition()
	v.checkWorkflows()
	v.checkSegments()
	v.checkSteps()
	v.checkAgents()
	v.checkDocs(skillsDir, "skills")
	v.checkDocs(principlesDir, "principles")
	return v.out
}

type validation struct {
	dir string
	out []health.Finding
	// def is remote.toml as decoded; nil when it did not decode.
	def *Definition
	// states is every status of the remote's own workflow that parsed.
	states map[string]bool
	// routes is the statuses of each route that built, keyed by the done.toml
	// section that governs it ("*" is the wildcard section).
	routes map[string]map[string]bool
	// docs is the remote's own workflow directory as agentvalidate reads it.
	docs []docindex.Doc
}

func (v *validation) fail(file, key, msg string) {
	loc := file
	if key != "" {
		loc += ": " + key
	}
	v.out = append(v.out, health.Error(health.IDRemoteDefinition, "Remote definition error", loc+": "+msg).About(file))
}

func (v *validation) read(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(v.dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			v.fail(rel, "", "file is missing")
		} else {
			v.fail(rel, "", "cannot be read: "+err.Error())
		}
		return "", false
	}
	return string(b), true
}

// checkDefinition judges remote.toml: shape, then the credential rule.
func (v *validation) checkDefinition() {
	body, ok := v.read(DefinitionFile)
	if !ok {
		return
	}
	def, md, err := decode(body)
	if err != nil {
		v.fail(DefinitionFile, "parse", err.Error())
		return
	}
	v.def = &def
	for _, k := range md.Undecoded() {
		v.fail(DefinitionFile, k.String(), "unknown key")
	}

	switch {
	case strings.TrimSpace(def.SatelleVersion) == "":
		v.fail(DefinitionFile, "satelle_version", "required: pin the satelle version as MAJOR.MINOR.PATCH")
	case !semver.Valid(def.SatelleVersion):
		v.fail(DefinitionFile, "satelle_version", fmt.Sprintf("%q is not a MAJOR.MINOR.PATCH version", def.SatelleVersion))
	}
	if strings.TrimSpace(def.Identity.Label) == "" {
		v.fail(DefinitionFile, "identity.label", "required: the label the host's platform understands")
	}
	for _, c := range []struct {
		key  string
		argv []string
	}{{"host.start", def.Host.Start}, {"host.network", def.Host.Network}, {"host.run", def.Host.Run}, {"host.stop", def.Host.Stop}} {
		v.checkArgv(c.key, c.argv)
	}
	if len(def.Host.Network) > 0 && !argvHas(def.Host.Network, PlaceholderAllow) {
		v.fail(DefinitionFile, "host.network", "must carry "+PlaceholderAllow+": the egress allowlist is passed there")
	}
	for i, argv := range def.Host.Bootstrap {
		v.checkArgv(fmt.Sprintf("host.bootstrap[%d]", i), argv)
	}
	v.checkLifecycle(def)
	v.checkRepos(def)
	v.checkAllow(def)
	if len(def.Host.Run) > 0 && !argvHasCommand(def.Host.Run) {
		v.fail(DefinitionFile, "host.run", "has no {command} placeholder; the host cannot run a step")
	}
	if c := def.Host.StoryCommand; c != "" && !strings.Contains(c, StoryPlaceholder) {
		v.fail(DefinitionFile, "host.story_command", "must carry "+StoryPlaceholder+", which stands for the id of the story it works")
	}
	if len(def.Segments) == 0 {
		v.fail(DefinitionFile, "segment", "required: name at least one lifecycle segment (entry and exit status)")
	}
	seen := map[string]int{}
	for i, t := range def.Triggers {
		key := fmt.Sprintf("trigger[%d]", i)
		switch {
		case strings.TrimSpace(t.Name) == "":
			v.fail(DefinitionFile, key+".name", "required: the name a fired trigger is addressed by")
		case !ValidName(t.Name):
			v.fail(DefinitionFile, key+".name", "must be lower-case letters, digits, - or _, starting with a letter or digit")
		default:
			if j, dup := seen[t.Name]; dup {
				v.fail(DefinitionFile, key+".name", fmt.Sprintf("duplicate of trigger[%d]", j))
			} else {
				seen[t.Name] = i
			}
		}
		if strings.TrimSpace(t.Kind) == "" {
			v.fail(DefinitionFile, key+".kind", "required")
		}
	}
	for i, s := range def.Secrets {
		if !envKeyPattern.MatchString(s) {
			v.fail(DefinitionFile, fmt.Sprintf("secrets[%d]", i), "is not a well-formed .env key name (A-Z, 0-9 and _)")
		}
	}
	if def.WakeKey != "" && !envKeyPattern.MatchString(def.WakeKey) {
		v.fail(DefinitionFile, "wake_key", "is not a well-formed .env key name (A-Z, 0-9 and _)")
	}

	var raw map[string]any
	if _, err := toml.Decode(body, &raw); err == nil {
		v.checkCredentials(raw)
	}
}

// CommandPlaceholder is the argv token host.run carries where the step's command
// goes.
const CommandPlaceholder = "{command}"

// HostArgv is one host command (host.start, host.network, host.run or host.stop)
// with {label}, {command} and {allow} filled in; allow is joined with commas. It
// is the single place the placeholders are resolved: whatever shows a line and
// whatever runs it call this, so they cannot disagree. Each placeholder is
// replaced in one pass, so a value that itself contains a placeholder is not
// expanded again.
func (d Definition) HostArgv(argv []string, command string, allow []string) []string {
	r := strings.NewReplacer("{label}", d.Identity.Label, CommandPlaceholder, command, PlaceholderAllow, strings.Join(allow, ","))
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = r.Replace(a)
	}
	return out
}

// HostRunArgv is host.run for command.
func (d Definition) HostRunArgv(command string) []string {
	return d.HostArgv(d.Host.Run, command, nil)
}

// StepsHoldingAllLegs names the steps that declare data, untrusted-input and
// outbound legs together, in definition order.
func (d Definition) StepsHoldingAllLegs() []string {
	var out []string
	for _, s := range d.Steps {
		if slices.Contains(s.Legs, LegData) && slices.Contains(s.Legs, LegUntrusted) && slices.Contains(s.Legs, LegOutbound) {
			out = append(out, s.Name)
		}
	}
	return out
}

func argvHasCommand(argv []string) bool {
	for _, a := range argv {
		if strings.Contains(a, CommandPlaceholder) {
			return true
		}
	}
	return false
}

func (v *validation) checkArgv(key string, argv []string) {
	if len(argv) == 0 {
		v.fail(DefinitionFile, key, "required: a non-empty command (argv list)")
		return
	}
	for i, a := range argv {
		if strings.TrimSpace(a) == "" {
			v.fail(DefinitionFile, fmt.Sprintf("%s[%d]", key, i), "is empty")
		}
	}
}

// checkCredentials refuses a credential-shaped key holding a literal value at
// any depth of remote.toml, by the rule the agents transport check already uses:
// a ${VAR} reference is accepted, anything else is a value. Only the two top
// level key-name lists are exempt — they hold names, and are shape-checked above.
func (v *validation) checkCredentials(raw map[string]any) {
	var walk func(path, key string, val any)
	walk = func(path, key string, val any) {
		switch t := val.(type) {
		case map[string]any:
			for _, k := range sortedKeys(t) {
				walk(join(path, k), k, t[k])
			}
		case []map[string]any:
			for i, item := range t {
				walk(fmt.Sprintf("%s[%d]", path, i), key, item)
			}
		case []any:
			for i, item := range t {
				walk(fmt.Sprintf("%s[%d]", path, i), key, item)
			}
		default:
			if config.SecretishKey(key) && config.CarriesValue(val) {
				v.fail(DefinitionFile, path, "credential-shaped key holds a literal value; hold a ${VAR} reference, or name a .env key in secrets")
			}
		}
	}
	for _, k := range sortedKeys(raw) {
		if k == "secrets" || k == "wake_key" {
			continue
		}
		walk(k, k, raw[k])
	}
}

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkWorkflows judges the remote's own done/step pair and records the
// statuses of every category whose route builds.
func (v *validation) checkWorkflows() {
	v.states = map[string]bool{}
	v.routes = map[string]map[string]bool{}
	done, okDone := v.read(doneFile)
	step, okStep := v.read(stepFile)
	if okDone {
		v.halfProblems("done", doneFile, done)
	}
	if okStep {
		v.halfProblems("step", stepFile, step)
	}
	v.docs = docindex.ReadWorkflowDocs(filepath.Join(v.dir, workflowsDir), nil)
	if !okDone || !okStep {
		return
	}
	lists, derr := wfdot.ParseDone(done)
	_, serr := wfdot.ParseSteps(step)
	if derr != nil || serr != nil {
		return // halfProblems already named the parse failure
	}
	for _, l := range lists {
		spec, err := wfdot.ParseRoute(done, step, l.Category, nil)
		if err != nil {
			v.fail(doneFile, "["+l.Category+"]", "route does not build: "+err.Error())
			continue
		}
		for _, p := range wfdot.Validate(spec) {
			v.fail(doneFile, "["+l.Category+"]", p)
		}
		route := map[string]bool{}
		for _, s := range spec.States {
			v.states[s.Name] = true
			route[s.Name] = true
		}
		v.routes[l.Category] = route
	}
}

// halfProblems applies the same structural check `satelle validate` applies to
// one half of a route, resolving skills against the remote's own skills only.
func (v *validation) halfProblems(name, rel, body string) {
	for _, p := range structure.Doc("workflows", name, body, v.skillExists) {
		v.fail(rel, "", p)
	}
}

func (v *validation) skillExists(name string) bool {
	_, err := os.Stat(filepath.Join(v.dir, skillsDir, name+".md"))
	return err == nil
}

// checkSegments holds every segment's entry and exit to the remote's own
// workflow: a status that only some other workflow declares is refused. A
// workflow that did not build declares no statuses, so it fails them all.
func (v *validation) checkSegments() {
	if v.def == nil {
		return // remote.toml did not decode; checkDefinition named why
	}
	known := make([]string, 0, len(v.states))
	for s := range v.states {
		known = append(known, s)
	}
	sort.Strings(known)
	declared := "it declares no statuses"
	if len(known) > 0 {
		declared = "it declares " + strings.Join(known, ", ")
	}
	declaredCats := make([]string, 0, len(v.routes))
	for c := range v.routes {
		declaredCats = append(declaredCats, c)
	}
	sort.Strings(declaredCats)
	for i, s := range v.def.Segments {
		// A category-scoped segment's statuses must be on each named category's
		// route; an unscoped one only on some route of the workflow.
		var scoped []map[string]bool
		var scopedNames []string
		for j, c := range s.Categories {
			key := fmt.Sprintf("segment[%d].categories[%d]", i, j)
			route, ok := v.routeFor(c)
			switch {
			case strings.TrimSpace(c) == "":
				v.fail(DefinitionFile, key, "is empty")
			case !ok:
				v.fail(DefinitionFile, key, fmt.Sprintf("%q is not a category of this remote's own workflow (it declares %s)", c, listOrNone(declaredCats, "no categories")))
			default:
				scoped = append(scoped, route)
				scopedNames = append(scopedNames, c)
			}
		}
		for _, f := range []struct{ key, status string }{{"entry", s.Entry}, {"exit", s.Exit}} {
			key := fmt.Sprintf("segment[%d].%s", i, f.key)
			switch {
			case strings.TrimSpace(f.status) == "":
				v.fail(DefinitionFile, key, "required")
			case !v.states[f.status]:
				v.fail(DefinitionFile, key, fmt.Sprintf("%q is not a status of this remote's own workflow (%s)", f.status, declared))
			default:
				for k, route := range scoped {
					if !route[f.status] {
						v.fail(DefinitionFile, key, fmt.Sprintf("%q is not a status of the %s route", f.status, scopedNames[k]))
					}
				}
			}
		}
		for j := 0; j < i; j++ {
			if o := v.def.Segments[j]; o.Entry == s.Entry && categoriesOverlap(o.Categories, s.Categories) {
				v.fail(DefinitionFile, fmt.Sprintf("segment[%d].entry", i), fmt.Sprintf("%q is also the entry of segment[%d] for an overlapping set of categories, so which segment takes a story would be ambiguous", s.Entry, j))
			}
		}
	}
}

// checkSteps is the only judge of probe form and step names: the preflight takes
// a definition that passed it. A probe needs a command and an expectation; a step
// is a status of the remote's own workflow; a leg is one the preflight knows.
func (v *validation) checkSteps() {
	if v.def == nil {
		return // remote.toml did not decode; checkDefinition named why
	}
	if v.def.Preflight.EnvProbe != nil {
		v.checkArgv("preflight.env_probe", v.def.Preflight.EnvProbe)
	}
	if v.def.Preflight.FileProbe != nil {
		v.checkArgv("preflight.file_probe", v.def.Preflight.FileProbe)
	}
	for i, s := range v.def.Steps {
		at := fmt.Sprintf("step[%d]", i)
		switch {
		case strings.TrimSpace(s.Name) == "":
			v.fail(DefinitionFile, at+".name", "required: a status of this remote's own workflow")
		case !v.states[s.Name]:
			v.fail(DefinitionFile, at+".name", fmt.Sprintf("%q is not a status of this remote's own workflow", s.Name))
		}
		for _, l := range s.Legs {
			if !slices.Contains(allLegs, l) {
				v.fail(DefinitionFile, at+".legs", fmt.Sprintf("%q is not a leg; use %s", l, strings.Join(allLegs, ", ")))
			}
		}
		for j, p := range s.Probes {
			pk := fmt.Sprintf("%s.probe[%d]", at, j)
			v.checkArgv(pk+".argv", p.Argv)
			switch p.Expect {
			case ExpectSucceed, ExpectRefused:
			case "":
				v.fail(DefinitionFile, pk+".expect", fmt.Sprintf("required: %s or %s", ExpectSucceed, ExpectRefused))
			default:
				v.fail(DefinitionFile, pk+".expect", fmt.Sprintf("%q is not %s or %s", p.Expect, ExpectSucceed, ExpectRefused))
			}
		}
	}
}

// routeFor is the route governing a category of the remote's own workflow: its
// own done.toml section, else the wildcard section.
func (v *validation) routeFor(category string) (map[string]bool, bool) {
	if r, ok := v.routes[category]; ok {
		return r, true
	}
	r, ok := v.routes[wfdot.WildcardCategory]
	return r, ok
}

func listOrNone(items []string, none string) string {
	if len(items) == 0 {
		return none
	}
	return strings.Join(items, ", ")
}

// categoriesOverlap reports whether two category sets can both take the same
// story; an empty set takes every category.
func categoriesOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// checkAgents judges the remote's bindings with the checks every agents layer
// gets: the transport check (nothing machine-specific, no credential value) and
// the binding validator, against the remote's own workflow only.
func (v *validation) checkAgents() {
	body, ok := v.read(agentsFile)
	if !ok {
		return
	}
	refusals, err := config.CheckAgentsTransport([]byte(body))
	if err != nil {
		v.fail(agentsFile, "parse", err.Error())
		return
	}
	for _, r := range refusals {
		v.fail(agentsFile, r.Section+"."+r.Field, r.Reason)
	}
	// Only the remote's own agents layer is judged: a local or workspace layer
	// belongs to a machine, and a definition does not carry one.
	repo, err := config.LoadAgents(v.dir)
	if err != nil {
		v.fail(agentsFile, "parse", err.Error())
		return
	}
	report := agentvalidate.ValidateEffectiveLayered(config.AgentLayers{Repo: repo}, config.GlobalAgentsConfig{}, nil, v.docs, v.skillBody)
	for _, f := range report.Findings {
		if f.Severity == health.SeverityError {
			key := f.Artifact
			if key == "" {
				key = "bindings"
			}
			v.fail(agentsFile, key, f.Detail)
		}
	}
}

func (v *validation) skillBody(name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(v.dir, skillsDir, name+".md"))
	return string(b), err == nil
}

// checkDocs applies the structural check to the remote's own skills or
// principles. An absent directory is an empty one.
func (v *validation) checkDocs(dirName, kind string) {
	entries, err := os.ReadDir(filepath.Join(v.dir, dirName))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !docindex.Indexable(e.Name()) {
			continue
		}
		rel := dirName + "/" + e.Name()
		b, err := os.ReadFile(filepath.Join(v.dir, dirName, e.Name()))
		if err != nil {
			v.fail(rel, "", "cannot be read: "+err.Error())
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		for _, p := range structure.Doc(kind, name, string(b), v.skillExists) {
			v.fail(rel, "", p)
		}
	}
}
