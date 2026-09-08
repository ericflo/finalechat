#!/usr/bin/env python3
"""Update the artifact/control OpenAPI sections from the versioned wire contract.

The existing chat API remains hand maintained. The contract tests discover
routes in every handler source file, including unique PUT operations.
"""
import json
from pathlib import Path
import re

root = Path(__file__).resolve().parents[1]
path = root / "docs/openapi.json"
doc = json.loads(path.read_text())
schemas = doc["components"]["schemas"]
ref = lambda n: {"$ref": "#/components/schemas/" + n}
string = {"type": "string"}
uuid = {"type": "string", "format": "uuid"}
obj = {"type": "object"}
array = lambda x: {"type": "array", "items": x}
def record(properties, required=()):
    return {"type": "object", "properties": properties, "required": list(required), "additionalProperties": False}

schemas["ArtifactChunk"] = record({"sha256": {"type":"string","pattern":"^[a-f0-9]{64}$"}, "size":{"type":"integer","minimum":1,"maximum":1048576}}, ["sha256","size"])
schemas["ArtifactFile"] = record({"path":string,"role":{"enum":["viewer","source","asset","derived","context"]},"content_type":string,"size":{"type":"integer","minimum":0,"maximum":536870912},"sha256":string,"chunks":array(ref("ArtifactChunk"))},["path","role","content_type","size","sha256","chunks"])
schemas["ArtifactManifest"] = record({"format":{"const":"finalechat.website/v1"},"producer":record({"name":string,"version":string},["name","version"]),"entrypoint":string,"settings_entrypoint":string,"dataset":obj,"viewer":obj,"captured_at":{"type":"string","format":"date-time"},"files":{"type":"array","maxItems":4096,"items":ref("ArtifactFile")}},["format","producer","entrypoint","dataset","captured_at","files"])
schemas["SettingsGrant"] = record({"key":string,"label":string,"scope":{"enum":["project","project_local","user","profile","session"]},"operations":array(string),"classes":{"type":"array","items":{"enum":["preference","credential_reference","permissions","executable","cost"]}}},["key","label","scope","operations","classes"])
schemas["SettingsShape"] = record({"type":{"enum":["string","boolean","integer","number","array","object"]},"enum":array({}),"minimum":{"type":"number"},"maximum":{"type":"number"},"max_length":{"type":"integer","maximum":32768},"items":ref("SettingsShape"),"properties":{"type":"object","additionalProperties":ref("SettingsShape")},"required":array(string)},["type"])
schemas["SettingsField"] = record({"key":string,"label":string,"description":string,"schema":ref("SettingsShape"),"writable":{"type":"boolean"},"unset":{"type":"boolean"},"locked_reason":string,"class":string,"effective_when":{"enum":["immediate","next_turn","next_task","new_or_resumed_session","restart_required","unknown"]},"source":string},["key","label","schema","writable","unset","class","effective_when"])
schemas["SettingsDescriptor"] = record({"format":{"const":"finalechat.settings/v1"},"schema_version":string,"adapter_version":string,"fields":array(ref("SettingsField")),"actions":array(record({"operation":string,"label":string,"class":string,"parameters":ref("SettingsShape")},["operation","label","class","parameters"]))},["format","schema_version","adapter_version","fields"])
schemas["SettingsSnapshot"] = record({"version":string,"context":string,"saved":obj,"effective":obj,"runtime_known":{"type":"boolean"},"runtime_version":string,"details":obj},["version","context","saved","effective","runtime_known"])
schemas["SettingsProposal"] = record({"operation":string,"schema_version":string,"expected_version":string,"generation":string,"edits":array(record({"op":{"enum":["set","unset"]},"key":string,"value":{}},["op","key"])),"parameters":obj},["operation","schema_version","expected_version"])
schemas["SettingsCommand"] = {"type":"object","properties":{"id":uuid,"user_id":uuid,"resource_id":uuid,"connector_id":uuid,"artifact_id":{"type":["string","null"]},"revision_id":{"type":["string","null"]},"proposal":ref("SettingsProposal"),"proposal_sha256":string,"status":{"enum":["queued","executing","succeeded","rejected","conflicted","expired","cancelled","unknown"]},"claim_token":uuid,"claim_instance":string,"lease_until":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"attempts":{"type":"integer"},"result":{"type":["object","null"]}},"description":"Lease credentials appear only in connector claim/renew responses. An attempt above 1 permits reconciliation only, never blind repetition of an uncertain action."}
for tag in ["Artifacts","Integration settings"]:
    if not any(t["name"] == tag for t in doc["tags"]): doc["tags"].append({"name":tag})
codes=schemas["Error"]["properties"]["error"]["properties"]["code"]["enum"]
for code in ["upload_in_progress","storage_quota","connector_offline"]:
    if code not in codes:codes.append(code)
doc["components"]["securitySchemes"]["connectorAuth"]={"type":"http","scheme":"bearer","description":"Scoped fcc_ installation credential. Can only read its own pairing status, heartbeat, publish granted resources/bindings, claim, renew and acknowledge its commands."}

routes=[]
def endpoint(method,path,title,body=None,security="agent",status="200",description="",response=None):
    routes.append((method,path,title))
    op={"tags":["Artifacts" if "artifacts" in path and "binding" not in path else "Integration settings"],"summary":title,"description":description,"responses":{status:{"description":title,"content":{"application/json":{"schema":response or obj}}},"401":{"description":"Authentication required"},"403":{"description":"Scope, grant or browser-origin check failed"},"404":{"description":"Not found in this account or connector"},"409":{"description":"Version, generation, lease, idempotency or resource state conflict"},"422":{"description":"Invalid bounded protocol document"}}}
    op["security"] = [{"sessionCookie":[]}] if security=="browser" else [{"connectorAuth":[]}] if security=="connector" else [{"sessionCookie":[]},{"connectorAuth":[]}] if security=="both" else doc["security"]
    params=[]
    for param in re.findall(r"\{([^}]+)\}",path):params.append({"name":param,"in":"path","required":True,"schema":string,"description":"Account-scoped identifier; thread also accepts ext:external_id, revision also accepts current. File paths must exactly match the manifest."})
    if params:op["parameters"]=params
    if body is not None:op["requestBody"]={"required":True,"content":{"application/json":{"schema":body}}}
    doc["paths"].setdefault(path,{})[method.lower()]=op
    return op

endpoint("PUT","/threads/{thread}/artifacts/{key}","Register or rename a session artifact",record({"title":string},["title"]))
endpoint("GET","/threads/{thread}/artifacts","List a thread’s artifacts")
endpoint("GET","/artifacts/{id}","Artifact metadata, current manifest and limits")
endpoint("DELETE","/artifacts/{id}","Delete artifact and all revisions",security="browser",description="Referenced chunks are retained until no revision references them. A durable cleanup queue retries B2 removal, including orphan upload versions. Deleting an artifact or its thread cancels queued settings commands from that artifact and fences claimed commands as unknown; it cannot roll back a local write that already started. Completed results remain on the settings resource.")
endpoint("POST","/artifacts/{id}/blobs/check","Check which chunk hashes are absent",record({"hashes":array(string)},["hashes"]))
op=endpoint("PUT","/artifacts/{id}/blobs/{hash}","Upload a verified chunk",status="201",description="Raw bytes, 1 to 1,048,576 bytes. URL hash must match SHA-256. 200 if already present; 409 upload_in_progress; 413 quota/size limit.")
op["requestBody"]={"required":True,"content":{"application/octet-stream":{"schema":{"type":"string","format":"binary","maxLength":1048576}}}}
endpoint("POST","/artifacts/{id}/revisions","Atomically commit an immutable revision",record({"manifest":ref("ArtifactManifest"),"previous_revision_id":{"type":["string","null"]},"client_key":string},["manifest","client_key"]),status="201",description="Previous must equal current, or null for the first revision. The same key and manifest returns 200 even after newer revisions exist; key reuse with different content conflicts. All chunks must already exist for this owner.")
op=endpoint("GET","/artifacts/{id}/revisions","List immutable revision summaries",description="Newest first, at most 50. Continue with before=next_before.");op["parameters"].append({"name":"before","in":"query","schema":uuid})
endpoint("GET","/artifacts/{id}/revisions/{revision}","Read one revision manifest")
op=endpoint("GET","/artifacts/{id}/revisions/{revision}/files/{file}","Download exact file bytes or a bounded slice",description="file is a slash-containing manifest path. Optional offset and length are decimal byte counts. Always Content-Disposition: attachment with a restrictive CSP. Touched chunks are verified; complete reads also verify the file hash.")
op["parameters"] += [{"name":x,"in":"query","schema":{"type":"integer","minimum":0}} for x in ["offset","length"]]
op["responses"]["200"]["content"]={"application/octet-stream":{"schema":{"type":"string","format":"binary"}}}
op=endpoint("GET","/artifacts/{id}/revisions/{revision}/preview","Load a sandboxed website entrypoint",security="browser",description="Optional surface=settings selects settings_entrypoint. This is a separate opaque-origin document protected by response CSP and iframe sandbox=allow-scripts. Scripts have no general API/fetch bridge.");op["parameters"].append({"name":"surface","in":"query","schema":{"enum":["settings"]}});op["responses"]["200"]["content"]={"text/html":{"schema":string}}
op=endpoint("GET","/artifacts/{id}/revisions/{revision}/download","Download the portable ZIP archive",description="Contains manifest.json and every exact file under portable paths.");op["responses"]["200"]["content"]={"application/zip":{"schema":{"type":"string","format":"binary"}}}

endpoint("POST","/connectors","Request pairing and receive a scoped credential",record({"name":string,"provider":string,"requested_grants":array(ref("SettingsGrant"))},["name","provider","requested_grants"]),status="201",description="Pending pairing expires in 15 minutes. Returns connector, one-time secret and approval_url. An API token cannot approve its own request.")
endpoint("GET","/connectors","List paired and pending installations",security="browser")
endpoint("GET","/connectors/{connector}","Connector pairing state and grants",security="both",description="The browser also receives resources; a connector credential can read only its own status.")
endpoint("POST","/connectors/{connector}/approve","Approve a subset of requested grants",record({"grants":array(ref("SettingsGrant"))},["grants"]),security="browser")
endpoint("DELETE","/connectors/{connector}","Revoke access and cancel queued commands",security="browser",description="Executing work becomes outcome unknown; revocation cannot promise rollback of an action already started.")
endpoint("POST","/connectors/{connector}/heartbeat","Renew installation process ownership",record({"instance":string},["instance"]),security="connector",description="One random stable process identity owns a 90-second heartbeat lease. A different process must wait for expiry.")
endpoint("PUT","/connectors/{connector}/resources/{key}","Publish a granted resource descriptor and snapshot",record({"instance":string,"generation":string,"descriptor":ref("SettingsDescriptor"),"snapshot":ref("SettingsSnapshot")},["instance","descriptor","snapshot"]),security="connector",description="Only approved resource keys/scopes/capabilities. Session resources require a generation. Saved defaults and runtime-effective values are distinct; credentials must never be included.")
endpoint("PUT","/connectors/{connector}/bindings/{id}","Bind a current settings artifact to a resource",record({"resource_id":uuid,"revision_id":uuid},["resource_id","revision_id"]),security="connector",description="id is an owned artifact. The revision must be current and have a settings entrypoint. Existing bindings cannot be silently moved to another resource.")
endpoint("GET","/artifacts/{id}/settings-binding","Read the artifact’s current resource binding")
endpoint("POST","/artifacts/{id}/settings-surface","Open a current settings editing session",record({"revision_id":uuid},["revision_id"]),security="browser",status="201",description="Only a current bound revision can open. Returns lease with id, resource_id, generation and expires_at (30 minutes). Keep its id only in the trusted host, never the iframe. Publication can advance while this editor remains open; each command still checks ownership, binding, generation, grants and settings version. At most 32 active editing sessions per account.",response=record({"lease":record({"id":uuid,"resource_id":uuid,"generation":string,"expires_at":{"type":"string","format":"date-time"}},["id","resource_id","generation","expires_at"])},["lease"]))
endpoint("GET","/settings-resources/{resource}","Read latest settings snapshot and connector availability")
op=endpoint("GET","/settings-resources/{resource}/audit","Read durable settings audit history",description="Newest first, up to 100; continue with before=next_before.");op["parameters"].append({"name":"before","in":"query","schema":uuid})
endpoint("POST","/settings-resources/{resource}/commands","Queue a user-approved settings command",record({"client_key":string,"proposal":ref("SettingsProposal"),"artifact_id":uuid,"revision_id":uuid,"surface_lease_id":uuid,"ttl_seconds":{"type":"integer","minimum":15,"maximum":86400},"send_when_connected":{"type":"boolean"}},["client_key","proposal"]),security="browser",status="202",description="The trusted host Save/action sends an exact frozen proposal. Default TTL 300 seconds; session maximum 300. Offline queueing is opt-in for stable settings.apply only. A surface requires its current bound revision, or a valid surface_lease_id opened while that exact revision was current. Historical documents cannot obtain new leases. Idempotent retries return the original command even after lease expiry. A 202 means queued, not saved.",response=record({"command":ref("SettingsCommand"),"created":{"type":"boolean"}},["command","created"]))
endpoint("GET","/settings-resources/{resource}/draft","Read a saved proposal draft",security="browser")
endpoint("PUT","/settings-resources/{resource}/draft","Save a draft without scheduling execution",ref("SettingsProposal"),security="browser")
endpoint("DELETE","/settings-resources/{resource}/draft","Discard a proposal draft",security="browser")
op=endpoint("POST","/connectors/{connector}/commands/claim","Claim the next command with a renewable lease",record({"instance":string},["instance"]),security="connector",description="wait=0..25 seconds. Subscribe-before-query plus polling recovers lost notifications. At most one executing command per resource. Response includes command, resource, reconcile_only. Attempts >1 require local-journal reconciliation, never blind repetition.");op["parameters"].append({"name":"wait","in":"query","schema":{"type":"integer","minimum":0,"maximum":25}})
endpoint("POST","/commands/{command}/renew","Renew the current fenced execution lease",record({"claim_token":uuid,"instance":string},["claim_token","instance"]),security="connector",description="Extends by 60 seconds only while the connector, generation ownership and command deadline remain valid.")
endpoint("POST","/commands/{command}/result","Persist an execution outcome",record({"claim_token":uuid,"instance":string,"status":{"enum":["succeeded","rejected","conflicted","unknown"]},"result":obj},["claim_token","instance","status","result"]),security="connector",description="Exact retry is idempotent; old claim tokens are fenced. Result includes before/after versions and per-field saved/runtime_applied/effective_when facts. B2 publication failure must not undo a local save.")
endpoint("GET","/commands/{command}","Read durable command status and result",security="browser",response=record({"command":ref("SettingsCommand")},["command"]))
endpoint("POST","/commands/{command}/cancel","Cancel a command that has not started",security="browser",description="Executing commands cannot be cancelled by this endpoint; no rollback is implied.")
path.write_text(json.dumps(doc,indent=2,ensure_ascii=False)+"\n")

api=root/"docs/API.md";text=api.read_text();marker="<!-- artifact-control-contract -->";text=text.split(marker)[0].rstrip()
section="""

<!-- artifact-control-contract -->

## Durable artifacts and integration settings

Feature-detect `artifacts.v1` and `settings-control.v1` in `/me` or `/auth/status`.
Artifact bytes use the configured blob backend; immutable metadata and command
outcomes live in PostgreSQL. B2 keys need `listFiles` for complete cleanup of
upload versions whose responses were lost. Memory storage is for tests only.

The container format is `finalechat.website/v1`. Each manifest lists a standalone
HTML entrypoint, optional settings entrypoint, producer, capture time, native
dataset identity and files with full SHA-256 hashes and ordered 1 MiB chunks.
Files retain native bytes. Limits: 4,096 files, 16,384 chunk references, 512 MiB
per file, 2 GiB per revision, 10 GiB of unique bytes per account, 8 MiB per HTML
entrypoint, 1 MiB manifest/request. Paths must be portable and traversal-free.
Upload missing chunks before committing. Preserve the parent revision and
idempotency key in a durable publisher journal before sending a commit.

Embed `/sdk/finale-artifact.js` inline in exported HTML. `finale.ready` resolves
after the host connects or a local archive directory is chosen. The SDK exposes
`manifest()`, bounded `read(path,{offset,length})`, `chunks(path)`, `lines(path)`,
`text(path)` and `openLocalFiles()`. The host creates a fresh MessageChannel for
one iframe document. Files are scoped to its mounted artifact/revision. There
is no generic HTTP proxy, credential access or iframe-triggered command queue.
Downloads are inert; preview documents get their own restrictive response CSP.

Pair control independently from upload. API tokens may request pairing but
only a signed-in user may approve resource keys, scopes, operations and classes.
The resulting `fcc_` token cannot use chat, upload, account or browser commands.
Publish `finalechat.settings/v1` descriptors with bounded typed fields, saved and
effective values, provenance, locked reasons and effect timing. `settings.apply`
uses explicit set/unset edits. The adapter revalidates against fresh native state.
Keep credentials out of snapshots, proposals and results.

Settings pages call `finale.settings.read()` and `finale.settings.propose(p)` to
stage a proposal; the trusted FinaleChat Save/action controls submit it. Call
`finale.settings.clear()` when new edits invalidate a staged proposal; clearing
does not submit a command. Match `onResult` outcomes to their proposals and
preserve newer drafts when results arrive late. Saved
historical surfaces remain read only until the user opens current settings.
Drafts do not execute. Explicit send-when-connected applies only to stable
settings edits with a deadline. Durable commands have fenced renewable leases,
an append-only audit and explicit expired/conflicted/unknown outcomes. Adapters
must journal intent and reconcile save-before-ack crashes. No automatic retry
may repeat an uncertain paid action. A saved file does not prove runtime adoption.

Deleting an artifact, directly or through thread deletion, cancels its queued
settings commands and fences claimed commands with an `unknown` result. This
does not undo a local write that may already have started. Completed results
remain on the settings resource, and independently submitted resource commands
remain authorized. Account deletion removes the related audit along with the account.

"""
section += "| Endpoint | Purpose |\n| --- | --- |\n"+"".join(f"| `{m} {p}` | {title} |\n" for m,p,title in routes)
section += "\nSee the OpenAPI schemas for exact envelopes and constraints. SSE adds `artifact.updated`, `artifact.deleted`, `connector.updated`, `settings-resource.updated` and `command.updated`; these notify clients to reload the corresponding durable objects.\n"
api.write_text(text+section)
