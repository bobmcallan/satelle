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
	WakeKey  string    `toml:"wake_key"`
	Identity Identity  `toml:"identity"`
	Host     Host      `toml:"host"`
	Segments []Segment `toml:"segment"`
	Triggers []Trigger `toml:"trigger"`
}

// Identity is the host identity the remote runs under: a label the platform
// understands. satelle treats it as an opaque string.
type Identity struct {
	Label string `toml:"label"`
}

// Host is the user's own tooling for reaching the host, each an argv list. The
// placeholders {label} and {command} are filled by whatever later runs it.
type Host struct {
	Start []string `toml:"start"`
	Run   []string `toml:"run"`
	Stop  []string `toml:"stop"`
	// Env is the environment the host commands run under. A credential-shaped
	// name must hold a ${VAR} reference, never a value.
	Env map[string]string `toml:"env"`
}

// Segment is one stretch of the lifecycle the remote serves: an entry status and
// an exit status, both statuses of the remote's own workflow.
type Segment struct {
	Entry string `toml:"entry"`
	Exit  string `toml:"exit"`
}

// Trigger is one way the remote is started. Kind is opaque to satelle.
type Trigger struct {
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
	}{{"host.start", def.Host.Start}, {"host.run", def.Host.Run}, {"host.stop", def.Host.Stop}} {
		v.checkArgv(c.key, c.argv)
	}
	if len(def.Segments) == 0 {
		v.fail(DefinitionFile, "segment", "required: name at least one lifecycle segment (entry and exit status)")
	}
	for i, t := range def.Triggers {
		if strings.TrimSpace(t.Kind) == "" {
			v.fail(DefinitionFile, fmt.Sprintf("trigger[%d].kind", i), "required")
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
		for _, s := range spec.States {
			v.states[s.Name] = true
		}
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
	for i, s := range v.def.Segments {
		for _, f := range []struct{ key, status string }{{"entry", s.Entry}, {"exit", s.Exit}} {
			key := fmt.Sprintf("segment[%d].%s", i, f.key)
			switch {
			case strings.TrimSpace(f.status) == "":
				v.fail(DefinitionFile, key, "required")
			case !v.states[f.status]:
				v.fail(DefinitionFile, key, fmt.Sprintf("%q is not a status of this remote's own workflow (%s)", f.status, declared))
			}
		}
	}
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
