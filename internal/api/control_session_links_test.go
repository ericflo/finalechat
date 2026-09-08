package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/ericflo/finalechat/internal/auth"
	"github.com/ericflo/finalechat/internal/control"
	"github.com/google/uuid"
)

func TestSessionSettingsDiscoveryIsScopedAndReflectsLifecycle(t *testing.T) {
	f := newControlFixture(t)
	external := "eagent:" + uuid.NewString()
	thread := str(sub(f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": external, "title": "Runtime links"}), "thread"), "id")
	endpoint := "/api/v1/threads/" + thread + "/settings-resources"
	grant := control.Grant{Key: "session-runtime", Label: "Runtime", Scope: "session", Operations: []string{"settings.apply", "settings.refresh"}, Classes: []string{"preference"}}
	created := f.agent.must(201, "POST", "/api/v1/connectors", map[string]any{"name": "Session", "provider": "test", "requested_grants": []control.Grant{grant}})
	id := str(sub(created, "connector"), "id")
	connector := &client{t: t, http: &http.Client{}, token: str(created, "secret")}
	f.browser.must(200, "POST", "/api/v1/connectors/"+id+"/approve", map[string]any{"grants": []control.Grant{grant}})
	connector.must(200, "POST", "/api/v1/connectors/"+id+"/heartbeat", map[string]any{"instance": f.instance})
	descriptor := f.descriptor
	descriptor.Actions = nil
	descriptor.Fields = descriptor.Fields[:1]
	descriptor.Fields[0].EffectiveWhen = "next_task"
	snapshot := f.snapshot
	snapshot.RuntimeKnown = true
	snapshot.RuntimeVersion = "v1"
	snapshot.Details = map[string]any{"thread_external_id": external}
	publish := func(generation string) map[string]any {
		return connector.must(200, "PUT", "/api/v1/connectors/"+id+"/resources/"+grant.Key, map[string]any{"instance": f.instance, "generation": generation, "descriptor": descriptor, "snapshot": snapshot})
	}
	resource := str(sub(publish("first-runtime"), "resource"), "id")
	check := func(generation string, available bool) {
		t.Helper()
		rows := f.browser.must(200, "GET", endpoint, nil)["resources"].([]any)
		if len(rows) != 1 {
			t.Fatalf("links: %+v", rows)
		}
		row := rows[0].(map[string]any)
		if row["id"] != resource || row["generation"] != generation || row["available"] != available {
			t.Fatalf("wrong runtime: %+v", row)
		}
	}
	check("first-runtime", true)
	proposal := f.proposal()
	proposal.Generation = "first-runtime"
	commandEndpoint := "/api/v1/settings-resources/" + resource + "/commands"
	f.browser.must(422, "PUT", "/api/v1/settings-resources/"+resource+"/draft", proposal)
	f.browser.must(422, "POST", commandEndpoint, map[string]any{"client_key": uuid.NewString(), "proposal": proposal, "send_when_connected": true})
	// An identical hint on project defaults does not make them session controls.
	f.snapshot.Details = snapshot.Details
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/resources/"+f.grant.Key, map[string]any{"instance": f.instance, "descriptor": f.descriptor, "snapshot": f.snapshot})
	check("first-runtime", true)
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
	other.must(404, "GET", endpoint, nil)
	connector.must(403, "GET", endpoint, nil)
	if _, err := testPool.Exec(context.Background(), "UPDATE connectors SET last_seen_at=now()-interval '5 minutes' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	check("first-runtime", false)
	connector.must(200, "POST", "/api/v1/connectors/"+id+"/heartbeat", map[string]any{"instance": f.instance})
	snapshot.RuntimeKnown = false
	publish("first-runtime")
	check("first-runtime", false)
	f.browser.must(409, "POST", commandEndpoint, map[string]any{"client_key": uuid.NewString(), "proposal": proposal})
	snapshot.RuntimeKnown = true
	publish("replacement-runtime")
	check("replacement-runtime", true)
	f.browser.must(200, "DELETE", "/api/v1/connectors/"+id, nil)
	if rows := f.browser.must(200, "GET", endpoint, nil)["resources"].([]any); len(rows) != 0 {
		t.Fatal("revoked connector remained linked")
	}
	f.browser.must(200, "DELETE", "/api/v1/threads/"+thread, nil)
	f.browser.must(404, "GET", endpoint, nil)
}
