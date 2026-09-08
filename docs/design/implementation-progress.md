**Implementation progress**

Active goal: implement both approved designs across FinaleChat, eagent, Claude Code, and Codex. This is a working checklist, not a completion claim.

- [x] FinaleChat immutable artifact store, manifest validation, upload/commit/download/preview APIs and cleanup.
- [x] FinaleChat artifact container, scoped browser bridge, historical navigation and downloads.
- [ ] eagent portable export, replay/recovery, proactive publisher and shared projections.
- [x] FinaleChat connector pairing, scoped credentials, resources, durable commands and audit.
- [x] FinaleChat settings surface and trusted Save/actions.
- [ ] eagent shared settings service, version checks/locking, command executor and publication.
- [x] Shared Python artifact publishing/companion and portable integration viewers.
- [x] Claude Code installer/hooks, transcript/subagent capture and settings support.
- [x] Codex installer, supported capture/import and native settings adapter.
- [ ] Documentation/API/CLI synchronization and backward compatibility.
- [ ] Integration tests, failure/recovery/security checks, real browser and adapter validation.

Initial state: both repositories were clean except the approved, untracked design documents in FinaleChat. Existing local services on ports 7331 and 8787 are user services and must not be restarted or reconfigured for tests. Use isolated databases and test ports. No production deployment or account messages are needed for implementation validation.

Verified so far (September 7, 2026):

- Full FinaleChat Go suite passes against the dedicated artifact test database; TypeScript checks and web build pass.
- eagent settings/archive/integration packages pass race checks. Existing web/config behavior tests pass after extracting the shared settings service and pure projections.
- Chromium opened the exported example eagent session entirely offline: 2,662 native events, the actual Go reducer compiled to WASM, and correct replay at event 100. No browser errors.
- Chromium completed real HTTP/browser/connector/file/archive round trips for eagent, Claude Code, and Codex against the isolated port-18787 server and fixture account. Each produced a durable local audit and a new immutable archive revision.
- Nineteen Python adapter/recovery tests pass, including the installed Codex 0.153.4 native config API in an isolated native home. Conditional writes, unset semantics, stale versions, secret exclusion, duplicate delivery, save-before-ack reconciliation, unknown outcomes, subagent capture, source-byte recovery, malformed known-field suppression, and adapter upgrade versions are covered. No model turns were invoked.
- The repository Chromium fixture runner passes opaque sandbox/bridge checks, offline SHA-256 verification and corruption rejection, trusted Save/actions, complete long-text handling, UTF-8 limits, stale edit review, late acknowledgements, invalid proposal clearing, navigation disconnect, and publication while editing. It uses a temporary Vite port and no real account or database.
- Current full FinaleChat Go tests, vet, TypeScript checks, web/server builds and generated CLI/API synchronization pass. The Go suite was rerun after fixing a concurrent build/test collision: CLI generation now skips unchanged content and replaces changed files atomically.

Remaining work before completion:

- Finish eagent's richer configuration surface: shared prompt/bundle writers and typed resource actions are committed; conditional undo, route-test semantics, and a usable custom UI with the current local editor's breadth remain.
- Finish archive provenance, publication recovery on stale/deleted remote parents, and viewer/runtime asset build synchronization. Verified source recovery into a new directory is committed in eagent.
- Broaden native adapter capability discovery and document unsupported settings/runtime scopes accurately. Codex currently uses native user-config APIs and stored native rollouts; it does not control another desktop/terminal session. Claude scopes are user, project, and project-local, with saved/effective provenance and runtime adoption unconfirmed.
- Extend targeted recovery coverage for upload/claim failure and deletion/cleanup where missing. Repeatable repository browser checks now cover the SDK/iframe/settings controls; the real fixture-account/connector/native-file round trips were also validated manually.
- Finish CLI/API/skill/readme documentation, compatibility review, full final test/vet/build checks, and review the whole change. No deployment or pushes have been made.

Handoff checkpoint: eagent commit `e5573a7` includes the portable archive/shared-settings foundations and the regression-tested `EAGENT_FINALECHAT=off` fix. Full eagent tests, vet and build passed. The other agent may apply its pending `review7.md` harness/provider/config fixes on that commit; its `default_config` validation now belongs in `internal/settings/service.go`. While those fixes are in flight, continuing work is confined to FinaleChat. Remaining eagent presentation/actions work is explicitly documented in its integration guide.
