package workitem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Typed task inputs. A task declares named inputs (Item.Inputs); an execution of
// it carries values for them (Item.InputValues). satelle declares, validates and
// passes them on — it never interprets what a value means.

// Input types a declaration may name.
const (
	InputString = "string"
	InputInt    = "int"
	InputBool   = "bool"
)

// InputEnvPrefix prefixes every input's environment variable name; the rest is
// the upper-cased input name.
const InputEnvPrefix = "SATELLE_INPUT_"

// InputDecl is one declared input of a task.
type InputDecl struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

var inputNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ParseInputDecls reads the `inputs_decl:` frontmatter value: a comma list of
// `name:type[:required|optional]` entries (optional is the default). The name
// grammar keeps every entry free of `,`, `=` and whitespace, so the list is
// lossless. An empty line declares nothing.
func ParseInputDecls(line string) ([]InputDecl, error) {
	if strings.TrimSpace(line) == "" {
		return nil, nil
	}
	var out []InputDecl
	seen := map[string]bool{}
	for _, entry := range strings.Split(line, ",") {
		entry = strings.TrimSpace(entry)
		parts := strings.Split(entry, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("input %q must be name:type[:required|optional]", entry)
		}
		d := InputDecl{Name: parts[0], Type: parts[1]}
		if !inputNameRE.MatchString(d.Name) {
			return nil, fmt.Errorf("input name %q must match [a-z][a-z0-9_]*", d.Name)
		}
		switch d.Type {
		case InputString, InputInt, InputBool:
		default:
			return nil, fmt.Errorf("input %q has type %q: must be string, int or bool", d.Name, d.Type)
		}
		if len(parts) == 3 {
			switch parts[2] {
			case "required":
				d.Required = true
			case "optional":
			default:
				return nil, fmt.Errorf("input %q has %q: must be required or optional", d.Name, parts[2])
			}
		}
		// Names are lower-case, so the upper-cased env key collides only on an
		// identical name.
		if seen[d.Name] {
			return nil, fmt.Errorf("input %q is declared twice", d.Name)
		}
		seen[d.Name] = true
		out = append(out, d)
	}
	return out, nil
}

// FormatInputDecls is the inverse of ParseInputDecls (canonical form).
func FormatInputDecls(decls []InputDecl) string {
	parts := make([]string, 0, len(decls))
	for _, d := range decls {
		req := "optional"
		if d.Required {
			req = "required"
		}
		parts = append(parts, d.Name+":"+d.Type+":"+req)
	}
	return strings.Join(parts, ", ")
}

// CoerceInputs checks raw `name=value` strings against a task's declarations and
// returns them typed (string, int64 or bool). It refuses an undeclared name, a
// value that does not parse as its declared type, and a missing required input.
func CoerceInputs(decls []InputDecl, raw map[string]string) (map[string]any, error) {
	byName := make(map[string]InputDecl, len(decls))
	for _, d := range decls {
		byName[d.Name] = d
	}
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	out := map[string]any{}
	for _, n := range names {
		d, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("input %q is not declared by the task", n)
		}
		switch d.Type {
		case InputInt:
			v, err := strconv.ParseInt(raw[n], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("input %q wants an int, got %q", n, raw[n])
			}
			out[n] = v
		case InputBool:
			v, err := strconv.ParseBool(raw[n])
			if err != nil {
				return nil, fmt.Errorf("input %q wants a bool, got %q", n, raw[n])
			}
			out[n] = v
		default:
			out[n] = raw[n]
		}
	}
	for _, d := range decls {
		if _, ok := out[d.Name]; d.Required && !ok {
			return nil, fmt.Errorf("required input %q is missing (pass --input %s=<value>)", d.Name, d.Name)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// InputEnv renders input values as SATELLE_INPUT_<NAME> environment variables.
// Nil or empty values yield nil, so an input-less run adds nothing.
func InputEnv(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	env := make(map[string]string, len(values))
	for n, v := range values {
		env[InputEnvPrefix+strings.ToUpper(n)] = fmt.Sprint(v)
	}
	return env
}

// EncodeInputValues renders values as one canonical JSON line (sorted keys, no
// raw newlines), safe to hold in a single frontmatter or column value. Empty
// values encode as "".
func EncodeInputValues(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	b, _ := json.Marshal(values)
	return string(b)
}

// DecodeInputValues is the inverse of EncodeInputValues. Numbers come back as
// int64 (or float64 when not integral), so an int input survives a round trip
// as an int. An empty or malformed line decodes to nil.
func DecodeInputValues(s string) map[string]any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || len(m) == 0 {
		return nil
	}
	for k, v := range m {
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				m[k] = i
			} else if f, err := n.Float64(); err == nil {
				m[k] = f
			}
		}
	}
	return m
}

// InputsEqual reports whether two value maps encode identically.
func InputsEqual(a, b map[string]any) bool {
	return EncodeInputValues(a) == EncodeInputValues(b)
}
