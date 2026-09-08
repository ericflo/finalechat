package api

import (
	"github.com/ericflo/finalechat/internal/control"
	"net/http"
	"testing"
)

func TestOwnAgentConnectAndThreadDiscovery(t *testing.T) {
	f := newControlFixture(t)
	// A resource becomes discoverable from its conversation without an archive upload.
	f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": "eagent:linked", "title": "Linked session"})
	f.snapshot.Details = map[string]any{"thread_external_ids": []string{"eagent:linked"}}
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/resources/"+f.grant.Key, map[string]any{"instance": f.instance, "descriptor": f.descriptor, "snapshot": f.snapshot})
	out := f.browser.must(200, "GET", "/api/v1/threads/ext:eagent:linked/settings", nil)
	if got := len(out["resources"].([]any)); got != 1 {
		t.Fatalf("thread settings count=%d", got)
	}
	f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": "eagent:unlinked"})
	out = f.browser.must(200, "GET", "/api/v1/threads/ext:eagent:unlinked/settings", nil)
	if len(out["resources"].([]any)) != 0 {
		t.Fatal("unrelated conversation exposed settings")
	}
	f.connector.must(403, "GET", "/api/v1/threads/ext:eagent:linked/settings", nil)
	// Existing active grants are retained, and reconnect cannot undo disconnection.
	f.agent.must(200, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": f.connector.token})
	f.agent.must(404, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": "fcc_wrong"})
	f.connector.must(403, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": f.connector.token})
	f.browser.must(200, "DELETE", "/api/v1/connectors/"+f.id, nil)
	f.agent.must(404, "POST", "/api/v1/connectors/"+f.id+"/connect", map[string]any{"secret": f.connector.token})
	out = f.browser.must(200, "GET", "/api/v1/threads/ext:eagent:linked/settings", nil)
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
}
