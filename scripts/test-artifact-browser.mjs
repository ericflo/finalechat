#!/usr/bin/env node
// Uses a temporary Vite port and mocked API fixtures. No existing FinaleChat
// server, account, native settings, or database is accessed.
import assert from "node:assert/strict";
import { readFile, mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createHash } from "node:crypto";
import { createServer } from "../web/node_modules/vite/dist/node/index.js";
import react from "../web/node_modules/@vitejs/plugin-react/dist/index.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const moduleName = process.env.FINALECHAT_PLAYWRIGHT_MODULE || "playwright";
const { chromium } = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const sdk = await readFile(path.join(root, "sdk/finale-artifact.js"), "utf8");
const digest = (raw) => createHash("sha256").update(raw).digest("hex");
const content = "{\"event\":\"fixture\"}\n";
const manifest = { format: "finalechat.website/v1", producer: { name: "fixture", version: "1" }, entrypoint: "index.html", settings_entrypoint: "index.html", dataset: { settings_version: "v1" }, captured_at: "2026-09-07T00:00:00Z", files: [{ path: "session.jsonl", role: "source", content_type: "application/x-ndjson", size: Buffer.byteLength(content), sha256: digest(content), chunks: [{ sha256: digest(content), size: Buffer.byteLength(content) }] }] };
const revision = { id: "revision", artifact_id: "artifact", manifest, manifest_sha256: "fixture", created_at: manifest.captured_at };
const website = `<meta charset="utf-8"><button onclick="finale.openLocalFiles().catch(e=>document.body.dataset.error=e.message)">Open archive</button><script>${sdk}</script><script>window.ready=finale.ready;</script>`;
const temporary = await mkdtemp(path.join(tmpdir(), "finalechat-browser-fixtures-"));
const server = await createServer({ root: path.join(root, "web"), configFile: false, plugins: [react()], define: { __APP_VERSION__: '"browser-test"' }, server: { host: "127.0.0.1", port: 0, fs: { allow: [root] } }, logLevel: "error" });
let browser;
try {
  await server.listen();
  const address = server.httpServer.address();
  const base = `http://127.0.0.1:${address.port}`;
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.clock.install({ time: new Date("2026-09-07T12:00:00Z") });
  const errors = [], outbound = [], commands = [], reads = [];
  page.on("pageerror", (e) => errors.push(e.message));
  let commandResponse;
  let currentRevision = "revision";
  await page.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (url.origin !== base) { outbound.push(url.origin); return route.abort(); }
    const p = url.pathname;
    const json = (value) => route.fulfill({ json: value });
    if (p === "/offline") return route.fulfill({ contentType: "text/html", body: website });
    if (!p.startsWith("/api/")) return route.continue();
    if (p.endsWith("/draft")) return json({ proposal: null });
    if (p === "/api/v1/settings-resources/resource") return json(await page.evaluate(() => window.fixture));
    if (p === "/api/v1/settings-resources/resource/commands") {
      const body = route.request().postDataJSON(); commands.push(body);
      await new Promise((resolve) => { commandResponse = resolve; });
      return json({ created: true, command: { id: "command", resource_id: "resource", status: "succeeded", proposal: body.proposal, result: { effects: [{ effective_when: "new_or_resumed_session", runtime_applied: false }], undo: body.proposal.operation === "settings.apply" ? { format: "finalechat.settings-undo/v1", command_id: "command", restore_sha256: "a".repeat(64), operation: "settings.apply", edits: [{ op: "set", key: "/count", value: 2 }] } : undefined } } });
    }
    if (p === "/api/v1/artifacts/artifact") return json({ artifact: { id: "artifact", thread_id: "thread", title: "Fixture archive", current_revision_id: currentRevision }, revision: { ...revision, id: currentRevision } });
    if (p.endsWith("/settings-binding")) return json({ binding: { artifact_id: "artifact", resource_id: "resource", revision_id: currentRevision, generation: "" } });
    if (p.endsWith("/settings-surface")) return json({ lease: { id: "surface-lease", resource_id: "resource", generation: "", expires_at: "2030-01-01T00:00:00Z" } });
    if (p.endsWith("/revisions")) return json({ revisions: [] });
    if (p.endsWith("/preview")) return route.fulfill({ contentType: "text/html", body: website, headers: { "Content-Security-Policy": "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'none'; form-action 'none'; base-uri 'none'" } });
    if (p.endsWith("/files/session.jsonl")) { reads.push(p); return route.fulfill({ body: content }); }
    throw new Error(`Unexpected fixture API request: ${p}`);
  });

  await page.goto(`${base}/tests/artifacts.html`);
  await page.getByLabel("Concurrency", { exact: true }).fill("3");
  assert.equal(await page.getByLabel("Long prompt", { exact: true }).inputValue(), "A".repeat(4096), "editable strings were truncated");
  await page.getByLabel("Long prompt", { exact: true }).fill("B".repeat(4097));
  const save = page.getByRole("button", { name: "Save", exact: true });
  await save.click();
  await page.waitForFunction(() => document.querySelector(".settings-control button.primary")?.textContent === "Submitting…");
  assert.equal(commands.length, 1);
  assert.equal(commands[0].proposal.edits.find((e) => e.key === "/text").value.length, 4097);
  await page.getByLabel("Concurrency", { exact: true }).fill("4");
  commandResponse();
  await page.waitForFunction(() => document.querySelector(".command-result")?.textContent.includes("Saved"));
  assert.equal(await page.evaluate(() => window.staged.edits.find((e) => e.key === "/count").value), 4, "late result discarded newer input");
  await page.evaluate(() => { const next = structuredClone(window.fixture); next.resource.snapshot.version = "v2"; window.replaceFixture(next); });
  await page.getByLabel("Concurrency", { exact: true }).fill("5");
  assert.equal(await page.evaluate(() => window.staged.expected_version), "v1", "typing silently rebased stale edits");
  assert.equal(await save.isDisabled(), true);
  await page.getByRole("button", { name: "Review against current settings", exact: true }).click();
  assert.equal(await page.evaluate(() => window.staged.expected_version), "v2");
  assert.equal(await save.isEnabled(), true);
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  await page.getByRole("button", { name: "Review undo", exact: true }).click();
  assert.equal(commands.length, 1, "reviewing undo submitted a command");
  assert.equal(await page.evaluate(() => window.staged.operation), "settings.undo");
  assert.equal(await page.evaluate(() => window.staged.expected_version), "v2");
  assert.equal(await page.evaluate(() => window.staged.parameters.restore_sha256), "a".repeat(64));
  assert.match(await page.locator(".undo-review").innerText(), /Concurrency[\s\S]*2/);
  await page.getByRole("button", { name: "Discard changes", exact: true }).click();
  await page.getByText("Integration actions", { exact: true }).click();
  await page.getByLabel("Action", { exact: true }).selectOption("prompt.set");
  assert.equal(await page.getByRole("button", { name: "Save prompt override", exact: true }).isDisabled(), true);
  await page.getByLabel("name (required)", { exact: true }).selectOption('"narrator"');
  await page.getByLabel("text (required)", { exact: true }).fill("A complete prompt\n".repeat(200));
  assert.equal(await page.getByRole("button", { name: "Save prompt override", exact: true }).isEnabled(), true);
  assert.equal(await page.evaluate(() => window.staged.operation), "prompt.set");
  assert.equal(commands.length, 1, "choosing or typing an action submitted it");
  await page.getByLabel("text (required)", { exact: true }).fill("猫".repeat(12000));
  assert.equal(await page.getByRole("button", { name: "Save prompt override", exact: true }).isDisabled(), true, "UTF-8 byte limits were treated as character counts");
  console.log("PASS trusted Save, full text, stale versions, late acknowledgement, typed actions");

  await page.goto(`${base}/tests/artifacts.html?artifact=1`);
  await page.getByRole("button", { name: "Edit current settings", exact: true }).waitFor();
  let frame = page.frameLocator('iframe[title="Integration settings"]');
  await frame.locator("body").waitFor();
  let child = page.frames().find((f) => f.parentFrame());
  await child.evaluate(() => window.ready);
  assert.equal(await child.evaluate(() => { try { return !!parent.document; } catch { return false; } }), false, "opaque frame accessed application DOM");
  assert.match(await child.evaluate(() => finale.settings.read().then(() => "allowed", (e) => e.message)), /permission/, "historical view received settings capability");
  assert.equal(await child.evaluate(() => finale.text("session.jsonl")), content);
  assert.match(await child.evaluate(() => finale.read("../secret").then(() => "allowed", (e) => e.message)), /not in this archive/);
  assert.match(await child.evaluate(() => finale.reveal({ arbitrary: true }).then(() => "allowed", (e) => e.message)), /permission/);
  assert.equal(reads.length, 1);
  await page.getByRole("button", { name: "Edit current settings", exact: true }).click();
  await page.getByRole("button", { name: "Standard settings form", exact: true }).waitFor();
  await frame.locator("button").waitFor();
  child = page.frames().find((f) => f.parentFrame());
  await child.evaluate(() => window.ready);
  assert.equal(await child.evaluate(() => finale.settings.read().then((value) => JSON.stringify(value).includes("surface-lease"))), false, "iframe received trusted host lease identifier");
  currentRevision = "revision-two";
  await page.clock.fastForward(16000);
  await page.getByText("A newer archive is available.", { exact: false }).waitFor();
  await child.evaluate(async () => { const view = await finale.settings.read(); window.proposal = { operation: "settings.apply", schema_version: view.resource.descriptor.schema_version, expected_version: view.resource.snapshot.version, generation: "", edits: [{ op: "set", key: "/count", value: 6 }] }; await finale.settings.propose(window.proposal); });
  await save.waitFor();
  assert.equal(commands.length, 1, "iframe staged proposal submitted without human Save");
  await child.evaluate(() => finale.settings.propose({ ...window.proposal, edits: [{ op: "set", key: "/count", value: 100 }] }).catch(() => {}));
  assert.equal(await save.count(), 0, "invalid iframe input left an older proposal ready to submit");
  await child.evaluate(() => finale.settings.propose(window.proposal));
  await save.click();
  await page.waitForFunction(() => document.querySelector(".settings-control button.primary")?.textContent === "Submitting…");
  assert.equal(commands.length, 2);
  assert.equal(commands[1].revision_id, "revision");
  assert.equal(commands[1].surface_lease_id, "surface-lease");
  commandResponse();
  await page.waitForFunction(() => document.querySelector(".command-result")?.textContent.includes("Saved"));
  await child.evaluate(() => { location.href = "about:blank"; });
  await page.getByText("The viewer navigated away. Reopen this saved revision to reconnect it.", { exact: true }).waitFor();
  console.log("PASS opaque sandbox, historical reads, scoped files, staged-only iframe, invalid proposal clearing, publication during editing, navigation disconnect");

  await writeFile(path.join(temporary, "manifest.json"), JSON.stringify(manifest));
  await writeFile(path.join(temporary, "session.jsonl"), content);
  await page.goto(`${base}/offline`);
  let chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Open archive", exact: true }).click();
  await (await chooser).setFiles(temporary);
  await page.evaluate(() => window.ready);
  assert.equal(await page.evaluate(() => finale.text("session.jsonl")), content);
  await writeFile(path.join(temporary, "session.jsonl"), content.replace("fixture", "corrupt"));
  await page.evaluate(() => { window.localSelected = new Promise((resolve) => { const off = finale.on("local", () => { off(); resolve(); }); }); });
  chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Open archive", exact: true }).click();
  await (await chooser).setFiles(temporary);
  await page.evaluate(() => window.localSelected);
  assert.match(await page.evaluate(() => finale.text("session.jsonl").then(() => "accepted", (e) => e.message)), /Corrupt archive file/);
  assert.deepEqual(outbound, []);
  assert.deepEqual(errors, []);
  console.log("PASS extracted archive selection, offline Web Crypto verification, corruption rejection, no external requests");
} finally {
  await browser?.close();
  await server.close();
  await rm(temporary, { recursive: true, force: true });
}
