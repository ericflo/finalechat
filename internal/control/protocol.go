// Package control defines the bounded settings protocol shared by API and store.
// Adapters must validate again against fresh local state before executing.
package control

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/ericflo/finalechat/internal/artifact"
)

const Format = "finalechat.settings/v1"
const MaxDocumentBytes = 256 << 10
const MaxCommandBytes = 64 << 10

type Grant struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Scope      string   `json:"scope"`
	Operations []string `json:"operations"`
	Classes    []string `json:"classes"`
}

func (g Grant) Validate() error {
	if !artifact.ValidPath(g.Key) || strings.Contains(g.Key, "/") || len(g.Key) > 120 || g.Label == "" || len(g.Label) > 300 {
		return fmt.Errorf("invalid resource key or label")
	}
	if !slices.Contains([]string{"project", "project_local", "user", "profile", "session"}, g.Scope) {
		return fmt.Errorf("unsupported resource scope")
	}
	if len(g.Operations) == 0 || len(g.Operations) > 20 || len(g.Classes) > 10 {
		return fmt.Errorf("invalid grant")
	}
	for _, op := range g.Operations {
		if !slices.Contains([]string{"settings.apply", "settings.refresh", "settings.undo", "route.test", "bundle.save", "bundle.delete", "prompt.set", "prompt.reset", "connector.disable"}, op) {
			return fmt.Errorf("unsupported operation %q", op)
		}
	}
	for _, c := range g.Classes {
		if !slices.Contains([]string{"preference", "credential_reference", "permissions", "executable", "cost"}, c) {
			return fmt.Errorf("unsupported field class %q", c)
		}
	}
	return nil
}

// Shape is a deliberately bounded JSON Schema subset. No remote references,
// executable validators or regular expressions are accepted.
type Shape struct {
	Type       string           `json:"type"`
	Enum       []any            `json:"enum,omitempty"`
	Minimum    *float64         `json:"minimum,omitempty"`
	Maximum    *float64         `json:"maximum,omitempty"`
	MaxLength  int              `json:"max_length,omitempty"`
	Items      *Shape           `json:"items,omitempty"`
	Properties map[string]Shape `json:"properties,omitempty"`
	Required   []string         `json:"required,omitempty"`
}

func (s Shape) Check(depth int) error {
	if depth > 8 || !slices.Contains([]string{"string", "boolean", "integer", "number", "array", "object"}, s.Type) {
		return fmt.Errorf("unsupported or nested value type")
	}
	if len(s.Enum) > 1024 || len(s.Properties) > 256 || s.MaxLength < 0 || s.MaxLength > 32768 {
		return fmt.Errorf("value schema exceeds limits")
	}
	if s.Minimum != nil && s.Maximum != nil && *s.Minimum > *s.Maximum {
		return fmt.Errorf("invalid value range")
	}
	if s.Type == "array" {
		if s.Items == nil {
			return fmt.Errorf("array requires items")
		}
		if err := s.Items.Check(depth + 1); err != nil {
			return err
		}
	}
	for k, p := range s.Properties {
		if k == "" || len(k) > 120 {
			return fmt.Errorf("invalid property")
		}
		if err := p.Check(depth + 1); err != nil {
			return err
		}
	}
	for _, k := range s.Required {
		if _, ok := s.Properties[k]; !ok {
			return fmt.Errorf("unknown required property")
		}
	}
	return nil
}

func (s Shape) Validate(v any) error {
	if len(s.Enum) > 0 {
		raw, _ := json.Marshal(v)
		found := false
		for _, e := range s.Enum {
			b, _ := json.Marshal(e)
			if string(b) == string(raw) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("value is not in the allowed choices")
		}
	}
	switch s.Type {
	case "string":
		x, ok := v.(string)
		if !ok || len(x) > 32768 || (s.MaxLength > 0 && len(x) > s.MaxLength) {
			return fmt.Errorf("invalid or oversized text")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	case "number", "integer":
		var n float64
		switch x := v.(type) {
		case json.Number:
			var err error
			n, err = x.Float64()
			if err != nil {
				return fmt.Errorf("invalid number")
			}
		case float64:
			n = x
		default:
			return fmt.Errorf("expected number")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || (s.Type == "integer" && math.Trunc(n) != n) || (s.Minimum != nil && n < *s.Minimum) || (s.Maximum != nil && n > *s.Maximum) {
			return fmt.Errorf("number outside allowed range")
		}
	case "array":
		a, ok := v.([]any)
		if !ok || len(a) > 1024 || s.Items == nil {
			return fmt.Errorf("invalid array")
		}
		for _, x := range a {
			if err := s.Items.Validate(x); err != nil {
				return err
			}
		}
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object")
		}
		for _, k := range s.Required {
			if _, ok := m[k]; !ok {
				return fmt.Errorf("missing property %s", k)
			}
		}
		for k, x := range m {
			p, ok := s.Properties[k]
			if !ok {
				return fmt.Errorf("unknown property %s", k)
			}
			if err := p.Validate(x); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
	default:
		return fmt.Errorf("unsupported value type")
	}
	return nil
}

type Field struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Description   string `json:"description,omitempty"`
	Shape         Shape  `json:"schema"`
	Writable      bool   `json:"writable"`
	Unset         bool   `json:"unset"`
	LockedReason  string `json:"locked_reason,omitempty"`
	Class         string `json:"class"`
	EffectiveWhen string `json:"effective_when"`
	Source        string `json:"source,omitempty"`
}
type Action struct {
	Operation  string `json:"operation"`
	Label      string `json:"label"`
	Class      string `json:"class"`
	Parameters Shape  `json:"parameters"`
}
type Descriptor struct {
	Format         string   `json:"format"`
	SchemaVersion  string   `json:"schema_version"`
	AdapterVersion string   `json:"adapter_version"`
	Fields         []Field  `json:"fields"`
	Actions        []Action `json:"actions,omitempty"`
}
type Snapshot struct {
	Version        string         `json:"version"`
	Context        string         `json:"context"`
	Saved          map[string]any `json:"saved"`
	Effective      map[string]any `json:"effective"`
	RuntimeKnown   bool           `json:"runtime_known"`
	RuntimeVersion string         `json:"runtime_version,omitempty"`
	Details        map[string]any `json:"details,omitempty"`
}

func (d Descriptor) Validate(g Grant) error {
	if d.Format != Format || d.SchemaVersion == "" || len(d.SchemaVersion) > 200 || d.AdapterVersion == "" || len(d.Fields) > 512 || len(d.Actions) > 20 {
		return fmt.Errorf("invalid settings descriptor")
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if f.Key == "" || len(f.Key) > 200 || seen[f.Key] || f.Label == "" || len(f.Label) > 300 || len(f.Description) > 4000 {
			return fmt.Errorf("invalid field")
		}
		seen[f.Key] = true
		if f.Writable && !slices.Contains(g.Classes, f.Class) {
			return fmt.Errorf("field %s requires an ungranted capability", f.Key)
		}
		if !slices.Contains([]string{"immediate", "next_turn", "next_task", "new_or_resumed_session", "restart_required", "unknown"}, f.EffectiveWhen) {
			return fmt.Errorf("invalid effect timing")
		}
		if err := f.Shape.Check(0); err != nil {
			return fmt.Errorf("%s: %w", f.Key, err)
		}
	}
	seen = map[string]bool{}
	for _, a := range d.Actions {
		if seen[a.Operation] || a.Label == "" || !slices.Contains(g.Operations, a.Operation) || !slices.Contains(g.Classes, a.Class) {
			return fmt.Errorf("invalid or ungranted action")
		}
		seen[a.Operation] = true
		if a.Parameters.Type != "object" {
			return fmt.Errorf("action parameters must be an object")
		}
		if err := a.Parameters.Check(0); err != nil {
			return err
		}
	}
	return bounded(d, MaxDocumentBytes)
}

type Edit struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value any    `json:"value,omitempty"`
}
type Proposal struct {
	Operation       string         `json:"operation"`
	SchemaVersion   string         `json:"schema_version"`
	ExpectedVersion string         `json:"expected_version"`
	Generation      string         `json:"generation,omitempty"`
	Edits           []Edit         `json:"edits,omitempty"`
	Parameters      map[string]any `json:"parameters,omitempty"`
}

func (p Proposal) Validate(d Descriptor, g Grant) error {
	if p.SchemaVersion != d.SchemaVersion || p.ExpectedVersion == "" || len(p.ExpectedVersion) > 200 || len(p.Generation) > 200 || !slices.Contains(g.Operations, p.Operation) {
		return fmt.Errorf("unsupported operation or schema/version")
	}
	if err := bounded(p, MaxCommandBytes); err != nil {
		return err
	}
	if p.Operation == "settings.refresh" {
		if len(p.Edits) > 0 || len(p.Parameters) > 0 {
			return fmt.Errorf("refresh accepts no payload")
		}
		return nil
	}
	if p.Operation == "settings.apply" {
		if len(p.Edits) == 0 || len(p.Edits) > 512 || len(p.Parameters) > 0 {
			return fmt.Errorf("apply requires edits only")
		}
		fields := map[string]Field{}
		for _, f := range d.Fields {
			fields[f.Key] = f
		}
		seen := map[string]bool{}
		for _, e := range p.Edits {
			f, ok := fields[e.Key]
			if !ok || !f.Writable || seen[e.Key] || !slices.Contains(g.Classes, f.Class) {
				return fmt.Errorf("field %s is not writable", e.Key)
			}
			for key := range seen {
				if strings.HasPrefix(e.Key, key+"/") || strings.HasPrefix(key, e.Key+"/") {
					return fmt.Errorf("settings edits must not overlap")
				}
			}
			seen[e.Key] = true
			switch e.Op {
			case "unset":
				if !f.Unset || e.Value != nil {
					return fmt.Errorf("field %s cannot be unset this way", e.Key)
				}
			case "set":
				if err := f.Shape.Validate(e.Value); err != nil {
					return fmt.Errorf("%s: %w", e.Key, err)
				}
			default:
				return fmt.Errorf("unknown edit operation")
			}
		}
		return nil
	}
	if len(p.Edits) > 0 {
		return fmt.Errorf("actions do not accept edits")
	}
	for _, a := range d.Actions {
		if a.Operation == p.Operation {
			return a.Parameters.Validate(p.Parameters)
		}
	}
	return fmt.Errorf("operation unavailable")
}

func (s Snapshot) Validate() error {
	if s.Version == "" || len(s.Version) > 200 || len(s.Context) > 500 || s.Saved == nil || s.Effective == nil {
		return fmt.Errorf("invalid settings snapshot")
	}
	return bounded(s, MaxDocumentBytes)
}
func bounded(v any, n int) error {
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > n {
		return fmt.Errorf("document exceeds limits")
	}
	return nil
}
