package api

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSettingsSurfaceLeaseSurvivesPublicationButNotExpiryOrRetargeting(t *testing.T) {
	f := newControlFixture(t)
	base, manifest, files := artifactFixture(t)
	uploadFixture(t, base, manifest, files)
	first := f.agent.must(201, "POST", base+"/revisions", map[string]any{"manifest": manifest, "client_key": "surface-one"})
	revision := str(sub(first, "revision"), "id")
	artifactID := strings.TrimPrefix(base, "/api/v1/artifacts/")
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+artifactID, map[string]any{"resource_id": f.resource, "revision_id": revision})
	open := map[string]any{"revision_id": revision}
	f.agent.must(403, "POST", base+"/settings-surface", open)
	f.connector.must(403, "POST", base+"/settings-surface", open)
	lease := str(sub(f.browser.must(201, "POST", base+"/settings-surface", open), "lease"), "id")
	second := f.agent.must(201, "POST", base+"/revisions", map[string]any{"manifest": manifest, "client_key": "surface-two", "previous_revision_id": revision})
	newRevision := str(sub(second, "revision"), "id")
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+artifactID, map[string]any{"resource_id": f.resource, "revision_id": newRevision})
	f.browser.must(409, "POST", base+"/settings-surface", open) // An archived document cannot start editing.
	endpoint := "/api/v1/settings-resources/" + f.resource + "/commands"
	body := map[string]any{"client_key": uuid.NewString(), "proposal": f.proposal(), "artifact_id": artifactID, "revision_id": revision}
	f.browser.must(409, "POST", endpoint, body)
	body["surface_lease_id"] = uuid.NewString()
	f.browser.must(409, "POST", endpoint, body)
	body["surface_lease_id"] = lease
	body["revision_id"] = newRevision
	f.browser.must(409, "POST", endpoint, body) // The lease cannot be moved to another document.
	body["revision_id"] = revision
	other := newControlFixture(t)
	f.browser.must(409, "POST", "/api/v1/settings-resources/"+other.resource+"/commands", body)
	created := f.browser.must(202, "POST", endpoint, body)
	id := str(sub(created, "command"), "id")
	if _, err := testPool.Exec(context.Background(), "UPDATE settings_surface_leases SET expires_at=now()-interval '1 second' WHERE id=$1", lease); err != nil {
		t.Fatal(err)
	}
	// A lost response may still be reconciled after the editing session ends.
	if str(sub(f.browser.must(200, "POST", endpoint, body), "command"), "id") != id {
		t.Fatal("retry lost its original command identity")
	}
	body["client_key"] = uuid.NewString()
	f.browser.must(409, "POST", endpoint, body)
	open["revision_id"] = newRevision
	lease = str(sub(f.browser.must(201, "POST", base+"/settings-surface", open), "lease"), "id")
	body["surface_lease_id"], body["revision_id"] = lease, newRevision
	// A replaced runtime generation invalidates even an otherwise fresh proposal.
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/resources/"+f.grant.Key, map[string]any{"instance": f.instance, "descriptor": f.descriptor, "snapshot": f.snapshot, "generation": "replacement"})
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+artifactID, map[string]any{"resource_id": f.resource, "revision_id": newRevision})
	p := f.proposal()
	p.Generation = "replacement"
	body["proposal"] = p
	f.browser.must(409, "POST", endpoint, body)
}
