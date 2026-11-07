package control

import (
	"strings"
	"testing"
)

func TestProposalRejectsOverlappingEditsInEitherOrder(t *testing.T) {
	g := Grant{Operations: []string{"settings.apply"}, Classes: []string{"preference"}}
	d := Descriptor{SchemaVersion: "fixture/1"}
	for _, key := range []string{"/permissions", "/permissions/allow", "/permissions/allowOther", "/permissionsElse"} {
		d.Fields = append(d.Fields, Field{Key: key, Writable: true, Unset: true, Class: "preference", Shape: Shape{Type: "string"}})
	}
	for _, keys := range [][]string{{"/permissions", "/permissions/allow"}, {"/permissions/allow", "/permissions"}, {"/permissions", "/permissions"}} {
		p := Proposal{Operation: "settings.apply", SchemaVersion: d.SchemaVersion, ExpectedVersion: "v1"}
		for _, key := range keys {
			p.Edits = append(p.Edits, Edit{Key: key, Op: "unset"})
		}
		if err := p.Validate(d, g); err == nil {
			t.Fatalf("accepted overlapping edit keys %v", keys)
		}
	}
	p := Proposal{Operation: "settings.apply", SchemaVersion: d.SchemaVersion, ExpectedVersion: "v1", Edits: []Edit{{Key: "/permissions/allow", Op: "unset"}, {Key: "/permissions/allowOther", Op: "unset"}, {Key: "/permissionsElse", Op: "unset"}}}
	if err := p.Validate(d, g); err != nil {
		t.Fatalf("rejected distinct siblings: %v", err)
	}
}

func TestSessionStartIsAnActionWithAPrompt(t *testing.T) {
	g := Grant{Key: "project-x", Label: "x", Scope: "project", Operations: []string{"session.start"}, Classes: []string{"cost"}}
	if err := g.Validate(); err != nil {
		t.Fatalf("session.start should be a grantable operation: %v", err)
	}
	d := Descriptor{Format: Format, SchemaVersion: "fixture/1", AdapterVersion: "1", Fields: []Field{},
		Actions: []Action{{Operation: "session.start", Label: "Start a new session", Class: "cost", Parameters: Shape{Type: "object", Properties: map[string]Shape{"prompt": {Type: "string", MaxLength: 32768}}, Required: []string{"prompt"}}}}}
	if err := d.Validate(g); err != nil {
		t.Fatalf("descriptor with session.start: %v", err)
	}
	ok := Proposal{Operation: "session.start", SchemaVersion: "fixture/1", ExpectedVersion: "v1", Parameters: map[string]any{"prompt": "Build the thing"}}
	if err := ok.Validate(d, g); err != nil {
		t.Fatalf("valid session.start rejected: %v", err)
	}
	missing := Proposal{Operation: "session.start", SchemaVersion: "fixture/1", ExpectedVersion: "v1", Parameters: map[string]any{}}
	if err := missing.Validate(d, g); err == nil {
		t.Fatal("session.start without a prompt was accepted")
	}
	ungranted := Grant{Key: "project-x", Label: "x", Scope: "project", Operations: []string{"settings.refresh"}, Classes: []string{"cost"}}
	if err := ok.Validate(d, ungranted); err == nil {
		t.Fatal("session.start accepted without the operation granted")
	}
}

func TestGrantValidateAcceptsRouteClass(t *testing.T) {
	g := Grant{Key: "k-session-1", Label: "l", Scope: "session", Operations: []string{"settings.apply", "settings.refresh"}, Classes: []string{"preference", "route"}}
	if err := g.Validate(); err != nil {
		t.Fatalf("route class rejected: %v", err)
	}
	bad := g
	bad.Classes = []string{"nonsense"}
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown class accepted")
	}
}

func TestDescriptorSuggestions(t *testing.T) {
	g := Grant{Key: "k", Label: "l", Scope: "session", Operations: []string{"settings.apply"}, Classes: []string{"route"}}
	d := Descriptor{Format: Format, SchemaVersion: "s/1", AdapterVersion: "1",
		Fields: []Field{
			{Key: "/provider", Label: "Provider", Shape: Shape{Type: "string", Enum: []any{"a", "b"}}, Writable: true, Class: "route", EffectiveWhen: "immediate"},
			{Key: "/model", Label: "Model", Shape: Shape{Type: "string"}, Writable: true, Class: "route", EffectiveWhen: "immediate", Suggest: &Suggest{List: "models", GroupBy: "/provider"}},
		},
		Suggestions: map[string][]Suggestion{"models": {{Value: "m1", Label: "Model 1", Group: "a"}, {Value: "m2", Group: "b"}}},
	}
	if err := d.Validate(g); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Descriptor){
		"unknown list":     func(d *Descriptor) { d.Fields[1].Suggest = &Suggest{List: "nope"} },
		"unknown group_by": func(d *Descriptor) { d.Fields[1].Suggest = &Suggest{List: "models", GroupBy: "/missing"} },
		"non-string field": func(d *Descriptor) { d.Fields[1].Shape = Shape{Type: "integer"} },
		"empty value":      func(d *Descriptor) { d.Suggestions = map[string][]Suggestion{"models": {{Value: ""}}} },
		"too many":         func(d *Descriptor) { d.Suggestions = map[string][]Suggestion{"models": make([]Suggestion, 5001)} },
	} {
		bad := d
		bad.Fields = append([]Field{}, d.Fields...)
		mutate(&bad)
		if bad.Validate(g) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestActionInputs(t *testing.T) {
	g := Grant{Key: "k", Label: "l", Scope: "project", Operations: []string{"session.start"}, Classes: []string{"cost"}}
	params := Shape{Type: "object", Properties: map[string]Shape{"prompt": {Type: "string"}, "provider": {Type: "string", Enum: []any{"a"}}, "model": {Type: "string"}, "n": {Type: "integer"}}}
	d := Descriptor{Format: Format, SchemaVersion: "s/1", AdapterVersion: "1",
		Actions:     []Action{{Operation: "session.start", Label: "Start", Class: "cost", Parameters: params, Inputs: []Input{{Name: "provider", Label: "Provider"}, {Name: "model", Label: "Model", Suggest: &Suggest{List: "models", GroupBy: "provider"}}}}},
		Suggestions: map[string][]Suggestion{"models": {{Value: "m", Group: "a"}}},
	}
	if err := d.Validate(g); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]Input{
		"unknown parameter": {{Name: "nope", Label: "x"}},
		"duplicate":         {{Name: "model", Label: "x"}, {Name: "model", Label: "y"}},
		"no label":          {{Name: "model"}},
		"unknown list":      {{Name: "model", Label: "x", Suggest: &Suggest{List: "nope"}}},
		"non-string":        {{Name: "n", Label: "x", Suggest: &Suggest{List: "models"}}},
		"unknown group":     {{Name: "model", Label: "x", Suggest: &Suggest{List: "models", GroupBy: "prompt"}}},
	} {
		bad := d
		bad.Actions = []Action{d.Actions[0]}
		bad.Actions[0].Inputs = in
		if bad.Validate(g) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestFormLayoutAndImpliedValues(t *testing.T) {
	g := Grant{Key: "k", Label: "l", Scope: "project", Operations: []string{"session.start"}, Classes: []string{"cost"}}
	params := Shape{Type: "object", Properties: map[string]Shape{"prompt": {Type: "string"}, "config": {Type: "string", Enum: []any{"a", "b"}}, "model": {Type: "string"}}}
	d := Descriptor{Format: Format, SchemaVersion: "s/1", AdapterVersion: "1",
		Fields: []Field{{Key: "/model", Label: "Model", Shape: Shape{Type: "string"}, Class: "cost", EffectiveWhen: "immediate", Section: "Models", Group: "Orchestrator", Advanced: true}},
		Actions: []Action{{Operation: "session.start", Label: "Start", Class: "cost", Parameters: params, Inputs: []Input{
			{Name: "config", Label: "Configuration", Default: "a", Suggest: &Suggest{List: "configs"}},
			{Name: "model", Label: "Model", Group: "Orchestrator", Default: "m1"},
		}}},
		Suggestions: map[string][]Suggestion{"configs": {{Value: "a", Label: "A", Detail: "runs m1", Implies: map[string]any{"model": "m1"}}, {Value: "b", Implies: map[string]any{"model": "m2", "n": 3.0, "on": true}}}},
	}
	if err := d.Validate(g); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Descriptor){
		"default outside the enum":  func(d *Descriptor) { d.Actions[0].Inputs[0].Default = "c" },
		"default of the wrong type": func(d *Descriptor) { d.Actions[0].Inputs[1].Default = 4.0 },
		"long section":              func(d *Descriptor) { d.Fields[0].Section = strings.Repeat("s", 81) },
		"long group":                func(d *Descriptor) { d.Fields[0].Group = strings.Repeat("g", 121) },
		"long detail":               func(d *Descriptor) { d.Suggestions["configs"][0].Detail = strings.Repeat("d", 301) },
		"nested implied value":      func(d *Descriptor) { d.Suggestions["configs"][0].Implies = map[string]any{"model": map[string]any{}} },
		"empty implied key":         func(d *Descriptor) { d.Suggestions["configs"][0].Implies = map[string]any{"": "x"} },
	} {
		bad := d
		bad.Fields = append([]Field{}, d.Fields...)
		bad.Actions = []Action{d.Actions[0]}
		bad.Actions[0].Inputs = append([]Input{}, d.Actions[0].Inputs...)
		bad.Suggestions = map[string][]Suggestion{"configs": append([]Suggestion{}, d.Suggestions["configs"]...)}
		mutate(&bad)
		if bad.Validate(g) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
