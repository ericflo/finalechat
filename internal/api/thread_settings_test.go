package api

import (
	"github.com/ericflo/finalechat/internal/control"
	"github.com/google/uuid"
	"net/http"
	"testing"
)

func TestOwnAgentConnectAndThreadDiscovery(t *testing.T) {
	f := newControlFixture(t)
	// Unique ids per run so -count=N repeats in one process (shared schema)
	// create fresh threads instead of colliding with earlier iterations.
	linked := "eagent:linked:" + uuid.NewString()
	unlinked := "eagent:unlinked:" + uuid.NewString()
	// A resource becomes discoverable from its conversation without an archive upload.
	f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": linked, "title": "Linked session"})
	f.snapshot.Details = map[string]any{"thread_external_ids": []string{linked}}
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/resources/"+f.grant.Key, map[string]any{"instance": f.instance, "descriptor": f.descriptor, "snapshot": f.snapshot})
	out := f.browser.must(200, "GET", "/api/v1/threads/ext:"+linked+"/settings", nil)
	if got := len(out["resources"].([]any)); got != 1 {
		t.Fatalf("thread settings count=%d", got)
	}
	f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": unlinked})
	out = f.browser.must(200, "GET", "/api/v1/threads/ext:"+unlinked+"/settings", nil)
	if len(out["resources"].([]any)) != 0 {
		t.Fatal("unrelated conversation exposed settings")
	}
	f.connector.must(403, "GET", "/api/v1/threads/ext:"+linked+"/settings", nil)
	// Existing active grants are retained, and reconnect cannot undo disconnection.
	f.agent.must(200, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": f.connector.token})
	f.agent.must(404, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": "fcc_wrong"})
	f.connector.must(403, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": f.connector.token})
	f.browser.must(200, "DELETE", "/api/v1/connectors/"+f.id, nil)
	f.agent.must(404, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": f.connector.token})
	out = f.browser.must(200, "GET", "/api/v1/threads/ext:"+linked+"/settings", nil)
	if len(out["resources"].([]any)) != 0 {
		t.Fatal("disconnected settings remained linked")
	}
	// A new account-owned installation works immediately, with a scoped credential.
	created := f.agent.must(201, "POST", "/api/v1/connectors", map[string]any{"name": "Automatic", "provider": "eagent", "requested_grants": []control.Grant{f.grant}, "connect": true})
	if str(sub(created, "connector"), "state") != "active" {
		t.Fatal("automatic installation still pending")
	}
	scoped := &client{t: t, http: &http.Client{}, token: str(created, "secret")}
	scoped.must(200, "POST", "/api/v1/connectors/"+str(sub(created, "connector"), "id")+"/heartbeat", map[string]any{"instance": "automatic-instance"})
	// Old installations can migrate with both owner authentication and the local secret.
	pending := f.agent.must(201, "POST", "/api/v1/connectors", map[string]any{"name": "Legacy", "provider": "eagent", "requested_grants": []control.Grant{f.grant}})
	f.agent.must(200, "POST", "/api/v1/connectors/"+str(sub(pending, "connector"), "id")+"/connect", map[string]any{"secret": str(pending, "secret")})
	// An integration that learned a new capability extends its own grants on
	// reconnect: owner authentication plus the local secret, no second pairing.
	wider := f.grant
	wider.Operations = append(append([]string{}, f.grant.Operations...), "session.start")
	autoID := str(sub(created, "connector"), "id")
	out = f.agent.must(200, "POST", "/api/v1/connectors/"+autoID+"/connect", map[string]any{"secret": str(created, "secret"), "requested_grants": []control.Grant{wider}})
	granted := sub(out, "connector")["grants"].([]any)[0].(map[string]any)["operations"].([]any)
	found := false
	for _, op := range granted {
		found = found || op == "session.start"
	}
	if !found {
		t.Fatalf("reconnect with wider grants did not extend them: %v", granted)
	}
	// Without a new grant set the active grants stay exactly as they are.
	out = f.agent.must(200, "POST", "/api/v1/connectors/"+autoID+"/connect", map[string]any{"secret": str(created, "secret")})
	if got := len(sub(out, "connector")["grants"].([]any)[0].(map[string]any)["operations"].([]any)); got != len(granted) {
		t.Fatalf("plain reconnect changed the grants: %d vs %d", got, len(granted))
	}
	bad := wider
	bad.Operations = []string{"session.launch"}
	f.agent.must(422, "POST", "/api/v1/connectors/"+autoID+"/connect", map[string]any{"secret": str(created, "secret"), "requested_grants": []control.Grant{bad}})
}
