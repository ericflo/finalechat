package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ericflo/finalechat/internal/auth"
	"github.com/ericflo/finalechat/internal/control"
	"github.com/google/uuid"
)

// A conversation counts as running while its connector is connected and
// publishes a session resource for it with a known runtime; the stop the
// agent offers is listed with it and can be queued like any session control.
func TestLiveSessionsListRunningConversationsAndTheirStop(t *testing.T) {
	f := newControlFixture(t)
	running := "eagent:" + uuid.NewString()
	idle := "eagent:" + uuid.NewString()
	thread := str(sub(f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": running, "title": "Running"}), "thread"), "id")
	f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": idle, "title": "Not running"})
	f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"title": "No session at all"})

	stopGrant := control.Grant{Key: "session-live", Label: "Live session", Scope: "session", Operations: []string{"settings.refresh", "session.stop"}, Classes: []string{"preference"}}
	quietGrant := control.Grant{Key: "session-quiet", Label: "Quiet session", Scope: "session", Operations: []string{"settings.refresh"}, Classes: []string{"preference"}}
	created := f.agent.must(201, "POST", "/api/v1/connectors", map[string]any{"name": "Sessions", "provider": "test", "requested_grants": []control.Grant{stopGrant, quietGrant}})
	id := str(sub(created, "connector"), "id")
	connector := &client{t: t, http: &http.Client{}, token: str(created, "secret")}
	f.browser.must(200, "POST", "/api/v1/connectors/"+id+"/approve", map[string]any{"grants": []control.Grant{stopGrant, quietGrant}})
	connector.must(200, "POST", "/api/v1/connectors/"+id+"/heartbeat", map[string]any{"instance": f.instance})

	stop := control.Action{Operation: "session.stop", Label: "Stop session", Description: "Ends this session now. A reply resumes it.", Class: "preference", Parameters: control.Shape{Type: "object"}}
	descriptor := control.Descriptor{Format: control.Format, SchemaVersion: "runtime/1", AdapterVersion: "1", Fields: []control.Field{}, Actions: []control.Action{stop}}
	snapshot := func(external string, known bool) control.Snapshot {
		return control.Snapshot{Version: "r1", Saved: map[string]any{}, Effective: map[string]any{}, RuntimeKnown: known, Details: map[string]any{"thread_external_id": external}}
	}
	publish := func(key string, d control.Descriptor, s control.Snapshot, status int) map[string]any {
		t.Helper()
		return connector.must(status, "PUT", "/api/v1/connectors/"+id+"/resources/"+key, map[string]any{"instance": f.instance, "generation": "gen-1", "descriptor": d, "snapshot": s})
	}
	// An action the grant does not allow is refused at publication.
	publish(quietGrant.Key, descriptor, snapshot(idle, true), 422)
	resource := str(sub(publish(stopGrant.Key, descriptor, snapshot(running, true), 200), "resource"), "id")
	quiet := descriptor
	quiet.Actions = nil
	publish(quietGrant.Key, quiet, snapshot(idle, false), 200)

	live := func() []map[string]any {
		t.Helper()
		out := []map[string]any{}
		for _, row := range f.browser.must(200, "GET", "/api/v1/live-sessions", nil)["sessions"].([]any) {
			out = append(out, row.(map[string]any))
		}
		return out
	}
	rows := live()
	if len(rows) != 1 || rows[0]["thread_id"] != thread || rows[0]["resource_id"] != resource || rows[0]["connector_id"] != id || rows[0]["label"] != "Live session" {
		t.Fatalf("live sessions: %+v", rows)
	}
	if s := sub(rows[0], "stop"); s["label"] != "Stop session" || s["description"] != stop.Description {
		t.Fatalf("stop action: %+v", rows[0]["stop"])
	}
	// Agent tokens may read it too.
	if rows := f.agent.must(200, "GET", "/api/v1/live-sessions", nil)["sessions"].([]any); len(rows) != 1 {
		t.Fatalf("token view: %+v", rows)
	}

	// A running session without the action is listed, without a stop.
	publish(quietGrant.Key, quiet, snapshot(idle, true), 200)
	if rows := live(); len(rows) != 2 {
		t.Fatalf("both running: %+v", rows)
	} else {
		for _, row := range rows {
			if _, ok := row["stop"]; ok != (row["thread_id"] == thread) {
				t.Fatalf("stop offered where it should not be: %+v", row)
			}
		}
	}
	publish(quietGrant.Key, quiet, snapshot(idle, false), 200)

	// The stop is queued like any session control; it takes no parameters.
	endpoint := "/api/v1/settings-resources/" + resource + "/commands"
	proposal := control.Proposal{Operation: "session.stop", SchemaVersion: "runtime/1", ExpectedVersion: "r1", Generation: "gen-1", Parameters: map[string]any{}}
	bad := proposal
	bad.Parameters = map[string]any{"force": true}
	f.browser.must(409, "POST", endpoint, map[string]any{"client_key": uuid.NewString(), "proposal": bad})
	f.browser.must(422, "POST", endpoint, map[string]any{"client_key": uuid.NewString(), "proposal": proposal, "ttl_seconds": 600})
	queued := f.browser.must(202, "POST", endpoint, map[string]any{"client_key": uuid.NewString(), "proposal": proposal, "ttl_seconds": 60})
	claimed := connector.must(200, "POST", "/api/v1/connectors/"+id+"/commands/claim", map[string]any{"instance": f.instance})
	if str(sub(claimed, "command"), "id") != str(sub(queued, "command"), "id") || str(sub(sub(claimed, "command"), "proposal"), "operation") != "session.stop" {
		t.Fatalf("claimed: %+v", claimed)
	}

	// Another account sees none of it.
	if rows := newOtherBrowser(t).must(200, "GET", "/api/v1/live-sessions", nil)["sessions"].([]any); len(rows) != 0 {
		t.Fatalf("leaked: %+v", rows)
	}

	// The runtime reporting itself gone, the lease lapsing, a release and a
	// dropped grant each take it off the list.
	publish(stopGrant.Key, descriptor, snapshot(running, false), 200)
	if rows := live(); len(rows) != 0 {
		t.Fatalf("stopped runtime still listed: %+v", rows)
	}
	publish(stopGrant.Key, descriptor, snapshot(running, true), 200)
	if _, err := testPool.Exec(context.Background(), "UPDATE connectors SET last_seen_at=now()-interval '5 minutes' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if rows := live(); len(rows) != 0 {
		t.Fatalf("lapsed connector still listed: %+v", rows)
	}
	connector.must(200, "POST", "/api/v1/connectors/"+id+"/heartbeat", map[string]any{"instance": f.instance})
	if rows := live(); len(rows) != 1 {
		t.Fatalf("heartbeat did not bring it back: %+v", rows)
	}
	connector.must(200, "POST", "/api/v1/connectors/"+id+"/release", map[string]any{"instance": f.instance})
	if rows := live(); len(rows) != 0 {
		t.Fatalf("released connector still listed: %+v", rows)
	}
	connector.must(200, "POST", "/api/v1/connectors/"+id+"/heartbeat", map[string]any{"instance": f.instance})
	if _, err := testPool.Exec(context.Background(), "UPDATE connectors SET grants=$2 WHERE id=$1", id, `[{"key":"session-quiet","label":"Quiet session","scope":"session","operations":["settings.refresh"],"classes":["preference"]}]`); err != nil {
		t.Fatal(err)
	}
	if rows := live(); len(rows) != 0 {
		t.Fatalf("dropped grant still listed: %+v", rows)
	}
}

func TestSessionStopActionValidation(t *testing.T) {
	g := control.Grant{Key: "s", Label: "S", Scope: "session", Operations: []string{"session.stop"}, Classes: []string{"preference"}}
	if err := g.Validate(); err != nil {
		t.Fatalf("session.stop grant: %v", err)
	}
	d := control.Descriptor{Format: control.Format, SchemaVersion: "1", AdapterVersion: "1", Actions: []control.Action{{Operation: "session.stop", Label: "Stop session", Description: strings.Repeat("x", 4001), Class: "preference", Parameters: control.Shape{Type: "object"}}}}
	if err := d.Validate(g); err == nil {
		t.Fatal("oversized action description accepted")
	}
	d.Actions[0].Description = "Ends it."
	if err := d.Validate(g); err != nil {
		t.Fatalf("valid stop action: %v", err)
	}
}

// newOtherBrowser signs in a second account in a browser session.
func newOtherBrowser(t *testing.T) *client {
	t.Helper()
	hash, err := auth.HashPassword("other-password-long")
	if err != nil {
		t.Fatal(err)
	}
	u, err := testAPI.store.CreateUser(context.Background(), uuid.NewString()+"@test.invalid", hash, "Other")
	if err != nil {
		t.Fatal(err)
	}
	other := newBrowser(t)
	other.must(200, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": "other-password-long"})
	return other
}
