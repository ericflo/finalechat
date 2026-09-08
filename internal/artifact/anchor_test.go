package artifact

import "testing"

func TestSourceAnchorsAreBoundedDatasetHints(t *testing.T) {
	m := sampleManifest()
	m.Dataset["session_id"] = "session"
	m.Files = append(m.Files, File{Path: "session.jsonl", Role: "source", Size: 200})
	base := SourceAnchor{DatasetFormat: m.Dataset["format"].(string), SessionID: "session", Seq: 2}
	if err := base.Validate(m); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*SourceAnchor){
		func(a *SourceAnchor) { a.SessionID = "other" },
		func(a *SourceAnchor) { a.Seq = -1 },
		func(a *SourceAnchor) { a.Seq = 9007199254740992 },
		func(a *SourceAnchor) { a.Seq = 0 },
		func(a *SourceAnchor) { a.EventID = "also-an-id" },
		func(a *SourceAnchor) { a.Seq = 0; a.File = "index.html"; a.Line = 1 },
		func(a *SourceAnchor) { a.Seq = 0; a.File = "../session.jsonl"; a.Line = 1 },
		func(a *SourceAnchor) { a.Seq = 0; a.File = "session.jsonl"; a.Line = 201 },
	} {
		bad := base
		change(&bad)
		if err := bad.Validate(m); err == nil {
			t.Fatalf("accepted invalid anchor: %+v", bad)
		}
	}
	base.Seq, base.File, base.Line = 0, "session.jsonl", 2
	if err := base.Validate(m); err != nil {
		t.Fatal(err)
	}
}
