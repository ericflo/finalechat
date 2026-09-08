package control

import "testing"

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
