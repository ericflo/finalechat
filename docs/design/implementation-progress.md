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

- Generic `artifact pack` and `artifact upload` commands share durable publication recovery with native integrations. Tests cover packaging/recovery, changed-during-capture rejection, manifests, source-prefix guards and remote deletion. A standalone CLI/API round trip verifies multi-chunk uploads, unchanged revision reuse, updates, and exact source bytes from both historical revisions.

- Full FinaleChat Go suite passes against the dedicated artifact test database; TypeScript checks and web build pass.
- eagent settings/archive/integration packages pass race checks. Existing web/config behavior tests pass after extracting the shared settings service and pure projections.
- Chromium opened the exported example eagent session entirely offline: 2,662 native events, the actual Go reducer compiled to WASM, and correct replay at event 100. No browser errors.
- Chromium completed real HTTP/browser/connector/file/archive round trips for eagent, Claude Code, and Codex against the isolated port-18787 server and fixture account. Each produced a durable local audit and a new immutable archive revision.
- Thirty-one Python adapter/recovery/transport/website tests pass, including the installed Codex 0.153.4 native config API in an isolated native home. Conditional writes, unset semantics, stale versions, secret exclusion, duplicate delivery, save-before-ack reconciliation, unknown outcomes, subagent capture, source-byte recovery, malformed known-field suppression, and adapter upgrade versions are covered. Publication tests additionally cover lost acknowledgements, competing parents, truncated/divergent source rejection, explicit recreation after remote deletion, and capture with an unavailable settings API. Account and connector credentials cannot follow API redirects or foreign absolute download URLs. No model turns were invoked.
- The repository Chromium fixture runner passes opaque sandbox/bridge checks, offline SHA-256 verification and corruption rejection, trusted Save/actions, complete long-text handling, UTF-8 limits, stale edit review, late acknowledgements, invalid proposal clearing, navigation disconnect, and publication while editing. It uses a temporary Vite port and no real account or database.
- Artifact/thread deletion cancels bound queued settings commands and fences claimed commands as unknown in the deletion transaction. Regression tests cover both deletion paths, late renewals/results, preservation of completed outcomes and independent resource commands, and account cascade cleanup without recreating audit rows.
- Current full FinaleChat Go tests, vet, TypeScript checks, web/server builds and generated CLI/API synchronization pass. The Go suite was rerun after fixing a concurrent build/test collision: CLI generation now skips unchanged content and replaces changed files atomically.

Remaining work before completion:

- Eagent's richer configuration surface now embeds the actual local editor and stylesheet through the artifact SDK. Its Chromium regression opens a real synthetic export in an opaque iframe and directly from disk, covering model/numeric settings, prompts, named configuration save/delete/default selection, saved-route results, read-only grants, explicit proposal clearing and preservation of newer form/raw drafts on delayed results. Shared prompt/bundle writers, route tests and conditional undo pass their targeted checks. The isolated worktree still needs integration into the main checkout.
- Eagent's archive now records attachment completeness and explicit unavailable/size-mismatched references. Audit capture streams a confined, bounded complete-record prefix. Captured pricing is frozen independently of the viewer catalog; a real offline WASM test deliberately changes the exporter catalog and proves matching replay estimates. Historical invoices and unrecorded past prompt/configuration values are labeled unknown. Continue the final provenance/coverage audit alongside runtime-setting support.
- Eagent's isolated worktree matches the Python publisher's recovery behavior: lost acknowledgements reuse immutable capture identities, competing parents require a fresh capture preserving both source prefixes, and deletion pauses until explicit single-session recreation. Tests cover truncated/divergent histories and retained pending evidence. Renderer fingerprints participate in publication; Makefile build/test/install/release targets rebuild matching replay assets, and the generator has a verification mode. Verified source recovery into a new directory is committed in eagent.
- Broaden native adapter capability discovery and document unsupported settings/runtime scopes accurately. Codex currently uses native user-config APIs and stored native rollouts; it does not control another desktop/terminal session. Claude scopes are user, project, and project-local, with saved/effective provenance and runtime adoption unconfirmed.
- Extend targeted recovery coverage for upload/claim failure and deletion/cleanup where missing. Repeatable repository browser checks now cover the SDK/iframe/settings controls; the real fixture-account/connector/native-file round trips were also validated manually.
- Finish CLI/API/skill/readme documentation, compatibility review, full final test/vet/build checks, and review the whole change. No deployment or pushes have been made.

Handoff checkpoint: eagent commit `e5573a7` includes the portable archive/shared-settings foundations and the regression-tested `EAGENT_FINALECHAT=off` fix. The other agent landed its eleven `review7.md` fixes as `44617fe`. Route-settings commit `c942277` is on top of it; full eagent tests, vet, build and integration/settings/web race checks pass, and its working tree is clean for the other agent's narrow audit. Further changes use explicit staging paths. Remaining eagent presentation/actions work is explicitly documented in its integration guide.

Active eagent worktree: `/tmp/eagent-settings-completion` on `codex/settings-completion`, based on `c942277`. This keeps the main checkout clean for the other agent's narrow audit. Conditional undo and grant-independent configuration versions in that worktree pass the full Go suite, vet/build, and integration/settings/web race tests; they still need to be brought into the main checkout as part of the final integration.
