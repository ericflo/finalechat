package api

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A settings resource may own its editor's website itself, so a conversation
// and a new-session draft open the same page.
func TestResourceOwnsItsSettingsWebsite(t *testing.T) {
	f := newControlFixture(t)
	website := "/api/v1/settings-resources/" + f.resource + "/website"
	f.connector.must(403, "PUT", website, map[string]any{"title": "Eagent settings"})
	out := f.agent.must(200, "PUT", website, map[string]any{"title": "Eagent settings"})
	a := sub(out, "artifact")
	if a["thread_id"] != nil || str(a, "resource_id") != f.resource || str(a, "key") != "agent-settings" {
		t.Fatalf("resource website registered wrongly: %v", a)
	}
	artifactID := str(a, "id")
	base := "/api/v1/artifacts/" + artifactID
	// Registration is idempotent; a new title renames the same website.
	again := f.agent.must(200, "PUT", website, map[string]any{"title": "Renamed"})
	if str(sub(again, "artifact"), "id") != artifactID || str(sub(again, "artifact"), "title") != "Renamed" {
		t.Fatal("re-registration did not keep the resource's website")
	}
	// Nothing is bound until a current revision exists and is bound.
	if f.browser.must(200, "GET", "/api/v1/settings-resources/"+f.resource, nil)["website"] != nil {
		t.Fatal("unpublished website reported")
	}
	_, m, files := artifactFixture(t)
	uploadFixture(t, base, m, files)
	revision := str(sub(f.agent.must(201, "POST", base+"/revisions", map[string]any{"manifest": m, "client_key": "one"}), "revision"), "id")
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+artifactID, map[string]any{"resource_id": f.resource, "revision_id": revision})
	site := sub(f.browser.must(200, "GET", "/api/v1/settings-resources/"+f.resource, nil), "website")
	if str(site, "artifact_id") != artifactID || str(site, "revision_id") != revision {
		t.Fatalf("resource website not reported: %v", site)
	}
	// The preview and the settings surface work through the ordinary routes.
	f.browser.must(200, "GET", base+"/revisions/"+revision+"/preview?surface=settings", nil)
	// A linked conversation without its own archived editor opens the
	// resource's website...
	f.agent.must(201, "POST", "/api/v1/threads", map[string]any{"external_id": "eagent:site", "title": "Linked session"})
	f.snapshot.Details = map[string]any{"thread_external_ids": []string{"eagent:site"}}
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/resources/"+f.grant.Key, map[string]any{"instance": f.instance, "descriptor": f.descriptor, "snapshot": f.snapshot})
	links := f.browser.must(200, "GET", "/api/v1/threads/ext:eagent:site/settings", nil)["resources"].([]any)
	if len(links) != 1 || str(links[0].(map[string]any), "artifact_id") != artifactID || str(links[0].(map[string]any), "revision_id") != revision {
		t.Fatalf("conversation did not fall back to the resource website: %v", links)
	}
	// ...and its own page, once published and bound, takes precedence.
	own := f.agent.must(200, "PUT", "/api/v1/threads/ext:eagent:site/artifacts/agent-settings", map[string]any{"title": "Session settings"})
	ownID := str(sub(own, "artifact"), "id")
	uploadFixture(t, "/api/v1/artifacts/"+ownID, m, files)
	ownRevision := str(sub(f.agent.must(201, "POST", "/api/v1/artifacts/"+ownID+"/revisions", map[string]any{"manifest": m, "client_key": "own"}), "revision"), "id")
	f.connector.must(200, "PUT", "/api/v1/connectors/"+f.id+"/bindings/"+ownID, map[string]any{"resource_id": f.resource, "revision_id": ownRevision})
	links = f.browser.must(200, "GET", "/api/v1/threads/ext:eagent:site/settings", nil)["resources"].([]any)
	if str(links[0].(map[string]any), "artifact_id") != ownID {
		t.Fatalf("conversation's own editor was not preferred: %v", links)
	}
	if str(sub(f.browser.must(200, "GET", "/api/v1/settings-resources/"+f.resource, nil), "website"), "artifact_id") != artifactID {
		t.Fatal("a conversation's editor replaced the resource's own website")
	}
	// A resource website has no conversation to anchor messages in.
	anchor, _ := json.Marshal(map[string]any{"dataset_format": "test.jsonl/v1", "session_id": "sample", "event_id": "native-record"})
	f.browser.must(404, "GET", base+"/revisions/"+revision+"/message?anchor="+url.QueryEscape(string(anchor)), nil)
	// Other accounts cannot register a website for this resource or see it.
	testAPI.cfg.Signup = "open"
	limiter := testAPI.registerLimiter
	testAPI.registerLimiter = newRateLimiter(1000)
	defer func() { testAPI.cfg.Signup, testAPI.registerLimiter = "", limiter }()
	other := newBrowser(t)
	other.must(201, "POST", "/api/v1/auth/register", map[string]any{"email": "website-other@example.com", "password": "correct-horse-battery", "display_name": "Other"})
	other.must(404, "PUT", website, map[string]any{"title": "Intruder"})
	other.must(404, "GET", base, nil)
	// Bad titles and unknown resources are rejected.
	f.agent.must(422, "PUT", website, map[string]any{"title": strings.Repeat("x", 201)})
	f.agent.must(404, "PUT", "/api/v1/settings-resources/"+uuid.NewString()+"/website", map[string]any{"title": "Missing"})
	// Revoking the connector unlinks the editor from conversations.
	f.browser.must(200, "DELETE", "/api/v1/connectors/"+f.id, nil)
	if len(f.browser.must(200, "GET", "/api/v1/threads/ext:eagent:site/settings", nil)["resources"].([]any)) != 0 {
		t.Fatal("revoked connector's editor remained linked")
	}
}
