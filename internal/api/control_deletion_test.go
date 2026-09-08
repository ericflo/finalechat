package api

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDeletingSettingsSurfaceFencesPendingCommands(t *testing.T) {
	for _, target := range []string{"artifact", "thread"} {
		for _, initial := range []string{"queued", "executing", "succeeded"} {
			t.Run(target+"/"+initial, func(t *testing.T) {
				f := newControlFixture(t)
				base, manifest, files := artifactFixture(t)
				uploadFixture(t, base, manifest, files)
				out := f.agent.must(201, "POST", base+"/revisions", map[string]any{"manifest": manifest, "client_key": "delete-surface"})
				revision := str(sub(out, "revision"), "id")
				artifactID := strings.TrimPrefix(base, "/api/v1/artifacts/")
				f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+artifactID, map[string]any{"resource_id": f.resource, "revision_id": revision})
				lease := str(sub(f.browser.must(201, "POST", base+"/settings-surface", map[string]any{"revision_id": revision}), "lease"), "id")
				body := map[string]any{"client_key": uuid.NewString(), "proposal": f.proposal(), "artifact_id": artifactID, "revision_id": revision, "surface_lease_id": lease}
				created := f.browser.must(202, "POST", "/api/v1/settings-resources/"+f.resource+"/commands", body)
				commandID := str(sub(created, "command"), "id")
				commandBase := "/api/v1/commands/" + commandID
				var claim string
				result := map[string]any{"version": "v2"}
				if initial != "queued" {
					claimed := sub(f.claim(), "command")
					if str(claimed, "id") != commandID {
						t.Fatal("wrong command claimed")
					}
					claim = str(claimed, "claim_token")
					if initial == "succeeded" {
						f.connector.must(200, "POST", commandBase+"/result", map[string]any{"claim_token": claim, "instance": f.instance, "status": "succeeded", "result": result})
					}
				}
				// Project-default commands explicitly submitted outside this page
				// retain their own authorization and must not be cancelled with it.
				independent := str(sub(f.queue(uuid.NewString()), "command"), "id")
				deletePath := base
				if target == "thread" {
					artifact := sub(f.browser.must(200, "GET", base, nil), "artifact")
					deletePath = "/api/v1/threads/" + str(artifact, "thread_id")
				}
				f.browser.must(200, "DELETE", deletePath, nil)
				got := sub(f.browser.must(200, "GET", commandBase, nil), "command")
				want := map[string]string{"queued": "cancelled", "executing": "unknown", "succeeded": "succeeded"}[initial]
				if str(got, "status") != want {
					t.Fatalf("surface deletion left %s command in %s, want %s", initial, str(got, "status"), want)
				}
				if initial == "succeeded" {
					if str(sub(got, "result"), "version") != "v2" {
						t.Fatal("deletion replaced the known result")
					}
				} else if str(sub(got, "result"), "reason") != "artifact_deleted" {
					t.Fatal("deletion reason missing from durable result")
				}
				if initial == "executing" {
					f.connector.must(404, "POST", commandBase+"/renew", map[string]any{"claim_token": claim, "instance": f.instance})
					f.connector.must(409, "POST", commandBase+"/result", map[string]any{"claim_token": claim, "instance": f.instance, "status": "succeeded", "result": result})
				}
				var leases, audit int
				if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM settings_surface_leases WHERE id=$1", lease).Scan(&leases); err != nil {
					t.Fatal(err)
				}
				if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM settings_audit WHERE command_id=$1 AND event='artifact.deleted' AND detail->>'artifact_id'=$2 AND detail->>'status'=$3", commandID, artifactID, want).Scan(&audit); err != nil {
					t.Fatal(err)
				}
				if leases != 0 || (initial != "succeeded" && audit != 1) || (initial == "succeeded" && audit != 0) {
					t.Fatalf("wrong lease/audit cleanup: leases=%d, deletion events=%d", leases, audit)
				}
				if str(sub(f.claim(), "command"), "id") != independent {
					t.Fatal("deletion affected an independently authorized resource command")
				}
			})
		}
	}
}

func TestAccountDeletionWithSettingsArtifactsDoesNotRecreateAudit(t *testing.T) {
	f := newControlFixture(t)
	base, manifest, files := artifactFixture(t)
	uploadFixture(t, base, manifest, files)
	out := f.agent.must(201, "POST", base+"/revisions", map[string]any{"manifest": manifest, "client_key": "delete-account"})
	revision := str(sub(out, "revision"), "id")
	artifactID := strings.TrimPrefix(base, "/api/v1/artifacts/")
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+artifactID, map[string]any{"resource_id": f.resource, "revision_id": revision})
	f.browser.must(202, "POST", "/api/v1/settings-resources/"+f.resource+"/commands", map[string]any{"client_key": uuid.NewString(), "proposal": f.proposal(), "artifact_id": artifactID, "revision_id": revision})
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) // The shared test account remains for the other tests.
	if _, err := tx.Exec(ctx, "DELETE FROM users WHERE id=(SELECT user_id FROM connectors WHERE id=$1)", f.id); err != nil {
		t.Fatal("account cascade could not delete its settings artifacts:", err)
	}
	var remaining int
	if err := tx.QueryRow(ctx, "SELECT (SELECT count(*) FROM artifacts WHERE id=$1)+(SELECT count(*) FROM settings_commands WHERE connector_id=$2)+(SELECT count(*) FROM settings_audit WHERE resource_id=$3)", artifactID, f.id, f.resource).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("account cascade retained artifact or settings data")
	}
}
