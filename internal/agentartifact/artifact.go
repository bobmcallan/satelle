// Package agentartifact implements provider-neutral structured output artifacts
// for isolated workflow steps. Artifact policy is declared by skill frontmatter;
// this package supplies only generic parsing, decoding, and structural checks.
package agentartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrNoArtifact means the final response contained no canonical artifact
// envelope. Optional contracts may treat this as an absent result.
var ErrNoArtifact = fmt.Errorf("no structured artifact")

// Contract is the artifact output contract declared by one skill.
type Contract struct {
	Name           string
	Type           string
	Required       bool
	RequiredFields []string
	ACCoverage     bool
}

// Active reports whether the skill opted into structured artifact handling.
func (c Contract) Active() bool {
	return c.Name != "" || c.Type != "" || c.Required || len(c.RequiredFields) > 0 || c.ACCoverage
}

// Artifact is the canonical final-response envelope payload.
type Artifact struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Body string `json:"body"`
}

var numberedCriterion = regexp.MustCompile(`(?m)(?:^|\s)(\d+)[.)]\s+\S`)

// ParseContract reads the generic output_* keys from skill YAML frontmatter.
// A skill without any such keys remains on the legacy self-attachment path.
func ParseContract(skillBody string) (Contract, error) {
	fm, ok := frontmatter(skillBody)
	if !ok {
		return Contract{}, nil
	}
	get := func(key string) string { return scalar(fm, key) }
	c := Contract{
		Name: get("output_name"),
		Type: get("output_type"),
	}
	var err error
	if raw := get("output_required"); raw != "" {
		c.Required, err = strconv.ParseBool(raw)
		if err != nil {
			return Contract{}, fmt.Errorf("output_required: expected true or false, got %q", raw)
		}
	}
	fields := get("output_schema")
	if fields == "" {
		fields = get("output_fields")
	}
	c.RequiredFields = csv(fields)
	if raw := get("output_ac_coverage"); raw != "" {
		c.ACCoverage, err = strconv.ParseBool(raw)
		if err != nil {
			return Contract{}, fmt.Errorf("output_ac_coverage: expected true or false, got %q", raw)
		}
	}
	if !c.Active() {
		return Contract{}, nil
	}
	if strings.TrimSpace(c.Name) == "" {
		return Contract{}, fmt.Errorf("output_name: required when an output contract is declared")
	}
	if strings.TrimSpace(c.Type) == "" {
		return Contract{}, fmt.Errorf("output_type: required when an output contract is declared")
	}
	for _, field := range c.RequiredFields {
		if field != "body" && field != "name" && field != "type" {
			return Contract{}, fmt.Errorf("output_schema: unsupported field %q (supported: name,type,body)", field)
		}
	}
	return c, nil
}

// Decode extracts the last canonical {"artifact":{...}} object from a final
// agent response. Command and ACP outputs both use this seam after transport
// unwrapping/capture.
func Decode(out []byte) (Artifact, error) {
	var found *Artifact
	var shapeErr error
	for _, obj := range jsonObjects(out) {
		var envelope map[string]json.RawMessage
		if json.Unmarshal(obj, &envelope) != nil {
			continue
		}
		raw, ok := envelope["artifact"]
		if !ok {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			shapeErr = fmt.Errorf("artifact: expected object")
			continue
		}
		var a Artifact
		if v, ok := fields["name"]; ok {
			if err := json.Unmarshal(v, &a.Name); err != nil {
				shapeErr = fmt.Errorf("artifact.name: expected string")
				continue
			}
		}
		if v, ok := fields["type"]; ok {
			if err := json.Unmarshal(v, &a.Type); err != nil {
				shapeErr = fmt.Errorf("artifact.type: expected string")
				continue
			}
		}
		v, ok := fields["body"]
		if !ok {
			shapeErr = fmt.Errorf("artifact.body: required field missing")
			continue
		}
		if err := json.Unmarshal(v, &a.Body); err != nil {
			shapeErr = fmt.Errorf("artifact.body: expected string")
			continue
		}
		found = &a
	}
	if found != nil {
		return *found, nil
	}
	if shapeErr != nil {
		return Artifact{}, shapeErr
	}
	return Artifact{}, fmt.Errorf(`%w {"artifact":{...}} object in agent output`, ErrNoArtifact)
}

// Validate applies one skill's generic structural contract and fills omitted
// name/type values from substrate. It never judges semantic artifact quality.
func Validate(a Artifact, c Contract, acceptanceCriteria string) (Artifact, error) {
	if !c.Active() {
		return a, nil
	}
	if a.Name != "" && a.Name != c.Name {
		return Artifact{}, fmt.Errorf("artifact.name: want %q, got %q", c.Name, a.Name)
	}
	if a.Type != "" && a.Type != c.Type {
		return Artifact{}, fmt.Errorf("artifact.type: want %q, got %q", c.Type, a.Type)
	}
	a.Name, a.Type = c.Name, c.Type
	for _, field := range c.RequiredFields {
		switch field {
		case "name":
			if strings.TrimSpace(a.Name) == "" {
				return Artifact{}, fmt.Errorf("artifact.name: required field empty")
			}
		case "type":
			if strings.TrimSpace(a.Type) == "" {
				return Artifact{}, fmt.Errorf("artifact.type: required field empty")
			}
		case "body":
			if strings.TrimSpace(a.Body) == "" {
				return Artifact{}, fmt.Errorf("artifact.body: required field empty")
			}
		}
	}
	if c.Required && strings.TrimSpace(a.Body) == "" {
		return Artifact{}, fmt.Errorf("artifact.body: required field empty")
	}
	if c.ACCoverage {
		for _, m := range numberedCriterion.FindAllStringSubmatch(acceptanceCriteria, -1) {
			n := m[1]
			re := regexp.MustCompile(`(?im)(?:^|\b)AC\s*#?\s*` + regexp.QuoteMeta(n) + `\b|^\s*#{1,6}\s+.*\b` + regexp.QuoteMeta(n) + `\b|^\s*` + regexp.QuoteMeta(n) + `[.)]\s+`)
			if !re.MatchString(a.Body) {
				return Artifact{}, fmt.Errorf("artifact.body: missing acceptance criterion %s", n)
			}
		}
	}
	return a, nil
}

func frontmatter(body string) ([]string, bool) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return lines[1:i], true
		}
	}
	return nil, false
}

func scalar(lines []string, key string) string {
	prefix := key + ":"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == prefix || strings.HasPrefix(line, prefix+" ") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"'`)
		}
	}
	return ""
}

func csv(s string) []string {
	s = strings.TrimSpace(strings.Trim(s, "[]"))
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.Trim(strings.TrimSpace(part), `"'`); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func jsonObjects(b []byte) [][]byte {
	var out [][]byte
	for i := 0; i < len(b); i++ {
		if b[i] == '{' {
			if end := balancedEnd(b, i); end > i {
				out = append(out, bytes.Clone(b[i:end+1]))
			}
		}
	}
	return out
}

func balancedEnd(b []byte, i int) int {
	depth, inString, escaped := 0, false, false
	for j := i; j < len(b); j++ {
		c := b[j]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}
