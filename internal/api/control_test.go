package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ericflo/finalechat/internal/control"
	"github.com/google/uuid"
)

type controlFixture struct {
	browser, agent, connector *client
	id, instance, resource    string
	grant                     control.Grant
	descriptor                control.Descriptor
	snapshot                  control.Snapshot
}

func newControlFixture(t *testing.T) *controlFixture {
	t.Helper()
	b, a := setup(t)
	f := &controlFixture{browser: b, agent: a, instance: uuid.NewString(), grant: control.Grant{Key: "project-config", Label: "Test project", Scope: "project", Operations: []string{"settings.apply", "settings.refresh", "route.test"}, Classes: []string{"preference", "cost"}}}
	out := a.must(201, "POST", "/api/v1/connectors", map[string]any{"name": "Test connector", "provider": "test", "requested_grants": []control.Grant{f.grant}})
	f.id = str(sub(out, "connector"), "id")
	f.connector = &client{t: t, http: &http.Client{}, token: str(out, "secret")}
	if !strings.HasPrefix(f.connector.token, "fcc_") {
		t.Fatal("connector did not receive scoped credential")
	}
	a.must(403, "POST", "/api/v1/connectors/"+f.id+"/approve", map[string]any{"grants": []control.Grant{f.grant}})
	f.connector.must(403, "POST", "/api/v1/connectors/"+f.id+"/heartbeat", map[string]any{"instance": f.instance})
	b.must(200, "POST", "/api/v1/connectors/"+f.id+"/approve", map[string]any{"grants": []control.Grant{f.grant}})
	f.connector.must(200, "POST", "/api/v1/connectors/"+f.id+"/heartbeat", map[string]any{"instance": f.instance})
	min, max := float64(1), float64(16)
	f.descriptor = control.Descriptor{Format: control.Format, SchemaVersion: "test/1", AdapterVersion: "1", Fields: []control.Field{{Key: "concurrency", Label: "Concurrency", Shape: control.Shape{Type: "integer", Minimum: &min, Maximum: &max}, Writable: true, Unset: true, Class: "preference", EffectiveWhen: "new_or_resumed_session"}, {Key: "locked", Label: "Managed setting", Shape: control.Shape{Type: "string"}, Writable: false, Class: "permissions", EffectiveWhen: "unknown", LockedReason: "Managed policy"}}, Actions: []control.Action{{Operation: "route.test", Label: "Test route", Class: "cost", Parameters: control.Shape{Type: "object", Properties: map[string]control.Shape{"route": {Type: "string", Enum: []any{"primary"}}}, Required: []string{"route"}}}}}
	f.snapshot = control.Snapshot{Version: "v1", Context: "test-project", Saved: map[string]any{"concurrency": 2}, Effective: map[string]any{}, RuntimeKnown: false}
	published := f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/resources/"+f.grant.Key, map[string]any{"instance": f.instance, "descriptor": f.descriptor, "snapshot": f.snapshot})
	f.resource = str(sub(published, "resource"), "id")
	return f
}
func (f *controlFixture) proposal() control.Proposal {
	return control.Proposal{Operation: "settings.apply", SchemaVersion: "test/1", ExpectedVersion: "v1", Edits: []control.Edit{{Op: "set", Key: "concurrency", Value: 4}}}
}
func (f *controlFixture) queue(key string) map[string]any {
	return f.browser.must(202, "POST", "/api/v1/settings-resources/"+f.resource+"/commands", map[string]any{"client_key": key, "proposal": f.proposal()})
}
func (f *controlFixture) claim() map[string]any {
	return f.connector.must(200, "POST", "/api/v1/connectors/"+f.id+"/commands/claim", map[string]any{"instance": f.instance})
}

func TestControlPairingIsolationAndValidation(t *testing.T) {
	f := newControlFixture(t)
	c := f.connector
	for _, route := range []string{"/api/v1/me", "/api/v1/tokens", "/api/v1/threads", "/api/v1/settings", "/api/v1/connectors/" + uuid.NewString()} {
		status, _ := c.do("GET", route, nil)
		if status != 403 && status != 404 {
			t.Fatalf("scoped credential escaped: %s %d", route, status)
		}
	}
	c.must(403, "POST", "/api/v1/connectors/"+f.id+"/approve", map[string]any{"grants": []control.Grant{f.grant}})
	c.must(403, "PUT", "/api/v1/connectors/"+f.id+"/resources/unapproved", map[string]any{"instance": f.instance, "descriptor": f.descriptor, "snapshot": f.snapshot})
	c.must(409, "POST", "/api/v1/connectors/"+f.id+"/heartbeat", map[string]any{"instance": uuid.NewString()})
	endpoint := "/api/v1/settings-resources/" + f.resource + "/commands"
	f.agent.must(403, "POST", endpoint, map[string]any{"client_key": "token-mutation", "proposal": f.proposal()})
	status, _ := f.browser.do("POST", endpoint, map[string]any{"client_key": "csrf", "proposal": f.proposal()}, "Sec-Fetch-Site", "cross-site", "Origin", "null")
	if status != 403 {
		t.Fatal("cross-origin settings mutation accepted")
	}
	for _, edit := range []control.Edit{{Op: "set", Key: "concurrency", Value: 50}, {Op: "set", Key: "locked", Value: "off"}, {Op: "set", Key: "unknown", Value: "x"}, {Op: "unset", Key: "concurrency", Value: 9}} {
		p := f.proposal()
		p.Edits = []control.Edit{edit}
		f.browser.must(409, "POST", endpoint, map[string]any{"client_key": uuid.NewString(), "proposal": p})
	}
	p := f.proposal()
	p.ExpectedVersion = "old"
	f.browser.must(409, "POST", endpoint, map[string]any{"client_key": uuid.NewString(), "proposal": p})
	p = f.proposal()
	p.Generation = "old-runtime"
	f.browser.must(409, "POST", endpoint, map[string]any{"client_key": uuid.NewString(), "proposal": p})
	p = f.proposal()
	p.Operation = "route.test"
	p.Edits = nil
	p.Parameters = map[string]any{"route": "http://localhost/private"}
	f.browser.must(409, "POST", endpoint, map[string]any{"client_key": uuid.NewString(), "proposal": p})
}

func TestControlDurableCommandsAndFencedAcknowledgements(t *testing.T) {
	f := newControlFixture(t)
	key := uuid.NewString()
	created := f.queue(key)
	id := str(sub(created, "command"), "id")
	endpoint := "/api/v1/settings-resources/" + f.resource + "/commands"
	if _, ok := sub(created, "command")["claim_token"]; ok {
		t.Fatal("browser received lease credential")
	}
	retry := f.browser.must(200, "POST", endpoint, map[string]any{"client_key": key, "proposal": f.proposal()})
	if str(sub(retry, "command"), "id") != id {
		t.Fatal("retry duplicated command")
	}
	p := f.proposal()
	p.Edits[0].Value = 6
	f.browser.must(409, "POST", endpoint, map[string]any{"client_key": key, "proposal": p})
	claimed := f.claim()
	q := sub(claimed, "command")
	claim := str(q, "claim_token")
	if str(q, "id") != id || claimed["reconcile_only"] != false {
		t.Fatal("bad first claim", claimed)
	}
	f.queue(uuid.NewString())
	if f.claim()["command"] != nil {
		t.Fatal("claimed concurrent mutation for same resource")
	}
	base := "/api/v1/commands/" + id
	f.connector.must(404, "POST", base+"/renew", map[string]any{"claim_token": uuid.NewString(), "instance": f.instance})
	f.connector.must(200, "POST", base+"/renew", map[string]any{"claim_token": claim, "instance": f.instance})
	result := map[string]any{"previous_version": "v1", "version": "v2", "effects": []any{map[string]any{"key": "concurrency", "saved": true, "runtime_applied": false, "effective_when": "new_or_resumed_session"}}, "snapshot_publication": "pending"}
	body := map[string]any{"claim_token": claim, "instance": f.instance, "status": "succeeded", "result": result}
	f.connector.must(200, "POST", base+"/result", body)
	f.connector.must(200, "POST", base+"/result", body)
	got := f.browser.must(200, "GET", base, nil)
	if str(sub(got, "command"), "status") != "succeeded" {
		t.Fatal("success not durable")
	}
	if _, ok := sub(got, "command")["claim_token"]; ok {
		t.Fatal("browser can forge worker acknowledgement")
	}
	second := sub(f.claim(), "command")
	secondID := str(second, "id")
	oldClaim := str(second, "claim_token")
	if _, err := testPool.Exec(context.Background(), "UPDATE settings_commands SET lease_until=now()-interval '1 second' WHERE id=$1", secondID); err != nil {
		t.Fatal(err)
	}
	reclaimed := f.claim()
	if reclaimed["reconcile_only"] != true || str(sub(reclaimed, "command"), "claim_token") == oldClaim {
		t.Fatal("redelivery was not fenced reconciliation")
	}
	f.connector.must(409, "POST", "/api/v1/commands/"+secondID+"/result", map[string]any{"claim_token": oldClaim, "instance": f.instance, "status": "succeeded", "result": result})
	f.connector.must(200, "POST", "/api/v1/commands/"+secondID+"/result", map[string]any{"claim_token": str(sub(reclaimed, "command"), "claim_token"), "instance": f.instance, "status": "unknown", "result": map[string]any{"reason": "cannot_reconcile"}})
	audit := f.browser.must(200, "GET", "/api/v1/settings-resources/"+f.resource+"/audit", nil)
	if len(audit["audit"].([]any)) < 7 {
		t.Fatal("missing audit events")
	}
}

func TestControlOfflineDraftExpiryAndRevocation(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, "UPDATE connectors SET last_seen_at=now()-interval '5 minutes' WHERE id=$1", f.id); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/settings-resources/" + f.resource
	body := map[string]any{"client_key": uuid.NewString(), "proposal": f.proposal()}
	f.browser.must(409, "POST", base+"/commands", body)
	f.browser.must(200, "PUT", base+"/draft", f.proposal())
	if f.browser.must(200, "GET", base+"/draft", nil)["proposal"] == nil {
		t.Fatal("draft lost")
	}
	body["send_when_connected"] = true
	queued := f.browser.must(202, "POST", base+"/commands", body)
	id := str(sub(queued, "command"), "id")
	if _, err := testPool.Exec(ctx, "UPDATE settings_commands SET expires_at=now()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := testAPI.store.ExpireSettingsCommands(ctx); err != nil {
		t.Fatal(err)
	}
	if str(sub(f.browser.must(200, "GET", "/api/v1/commands/"+id, nil), "command"), "status") != "expired" {
		t.Fatal("offline command did not expire")
	}
	f.connector.must(200, "POST", "/api/v1/connectors/"+f.id+"/heartbeat", map[string]any{"instance": f.instance})
	queued = f.queue(uuid.NewString())
	id = str(sub(queued, "command"), "id")
	f.browser.must(200, "POST", "/api/v1/commands/"+id+"/cancel", map[string]any{})
	if f.claim()["command"] != nil {
		t.Fatal("cancelled command was delivered")
	}
	queued = f.queue(uuid.NewString())
	id = str(sub(queued, "command"), "id")
	f.browser.must(200, "DELETE", "/api/v1/connectors/"+f.id, nil)
	f.connector.must(401, "GET", "/api/v1/connectors/"+f.id, nil)
	if str(sub(f.browser.must(200, "GET", "/api/v1/commands/"+id, nil), "command"), "status") != "cancelled" {
		t.Fatal("revocation left queued command executable")
	}
}

func TestControlArchivedSurfaceCannotSubmit(t *testing.T) {
	f := newControlFixture(t)
	base, m, files := artifactFixture(t)
	uploadFixture(t, base, m, files)
	out := f.agent.must(201, "POST", base+"/revisions", map[string]any{"manifest": m, "client_key": "one"})
	revision := str(sub(out, "revision"), "id")
	artifactID := strings.TrimPrefix(base, "/api/v1/artifacts/")
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+artifactID, map[string]any{"resource_id": f.resource, "revision_id": revision})
	body := map[string]any{"client_key": uuid.NewString(), "proposal": f.proposal(), "artifact_id": artifactID, "revision_id": revision}
	f.browser.must(202, "POST", "/api/v1/settings-resources/"+f.resource+"/commands", body)
	f.agent.must(201, "POST", base+"/revisions", map[string]any{"manifest": m, "client_key": "two", "previous_revision_id": revision})
	body["client_key"] = uuid.NewString()
	f.browser.must(409, "POST", "/api/v1/settings-resources/"+f.resource+"/commands", body)
}
