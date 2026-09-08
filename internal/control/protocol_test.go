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
