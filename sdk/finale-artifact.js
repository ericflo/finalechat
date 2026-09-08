/* FinaleChat artifact SDK v1. Embed this file inline in a self-contained page.
 * It also works from an extracted ZIP: choose its directory or source files
 * with finale.openLocalFiles(). No web server, CDN or network is required. */
(() => {
  "use strict";
  let port, nonce, seq = 0, connected = false, manifest, localFiles = new Map(), verified = new Set();
  const pending = new Map(), listeners = new Map();
  let readyResolve;
  const ready = new Promise((resolve) => { readyResolve = resolve; });
  function emit(name, detail) { for (const fn of listeners.get(name) || []) fn(detail); }
  window.addEventListener("message", (event) => {
    const m = event.data;
    if (event.source !== window.parent || window.parent === window || connected || !m || m.type !== "finalechat.artifact.connect" || m.version !== 1 || typeof m.nonce !== "string" || event.ports.length !== 1) return;
    port = event.ports[0]; nonce = m.nonce; connected = true;
    port.onmessage = (event) => {
      const m = event.data;
      if (!m || m.nonce !== nonce) return;
      if (m.event) { emit(m.event, m.detail); return; }
      const call = pending.get(m.id); if (!call) return;
      pending.delete(m.id); clearTimeout(call.timer);
      if (m.error) call.reject(new Error(m.error)); else call.resolve(m.result);
    };
    port.start(); readyResolve(true); emit("connected", m);
  });
  async function rpc(method, params) {
    if (!connected) throw new Error("Open this page in FinaleChat for connected settings.");
    const id = String(++seq);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(new Error("FinaleChat did not respond. Reopen the viewer.")); }, 45000);
      pending.set(id, { resolve, reject, timer }); port.postMessage({ nonce, id, method, params });
    });
  }
  function on(name, fn) { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn); return () => listeners.get(name).delete(fn); }
  async function getManifest() { if (connected) return rpc("artifact.manifest"); if (manifest) return manifest; throw new Error("Choose the extracted archive directory first."); }
  async function read(path, options = {}) {
    const m = await getManifest(), file = m.files.find((f) => f.path === path);
    if (!file) throw new Error("File is not in this archive.");
    const offset = options.offset || 0, length = options.length === undefined ? file.size - offset : options.length;
    if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(length) || offset < 0 || length < 0 || offset + length > file.size) throw new Error("Invalid file range.");
    if (connected) return rpc("artifact.read", { path, offset, length });
    const local = localFiles.get(path); if (!local) throw new Error("Missing local file: " + path);
    if (!globalThis.crypto?.subtle) throw new Error("This browser cannot verify local archives. Use a browser with Web Crypto or verify with the FinaleChat CLI.");
    let position = 0;
    for (const chunk of file.chunks || []) {
      if (!Number.isSafeInteger(chunk.size) || chunk.size < 1 || chunk.size > 1048576 || !/^[a-f0-9]{64}$/.test(chunk.sha256)) throw new Error("Invalid archive chunk.");
      const end = position + chunk.size, key = path + ":" + position;
      if (end > offset && position < offset + length && !verified.has(key)) {
        const raw = await local.slice(position, end).arrayBuffer();
        const hash = [...new Uint8Array(await crypto.subtle.digest("SHA-256", raw))].map(x => x.toString(16).padStart(2, "0")).join("");
        if (hash !== chunk.sha256) throw new Error("Corrupt archive file: " + path);
        verified.add(key);
      }
      position = end;
    }
    if (position !== file.size) throw new Error("Invalid archive chunk lengths.");
    return local.slice(offset, offset + length).arrayBuffer();
  }
  async function* chunks(path) { const m = await getManifest(), file = m.files.find((f) => f.path === path); if (!file) throw new Error("Missing file: " + path); for (let offset = 0; offset < file.size; offset += 1048576) yield new Uint8Array(await read(path, { offset, length: Math.min(1048576, file.size - offset) })); }
  async function text(path) {
    const m = await getManifest(), file = m.files.find((f) => f.path === path);
    if (!file || file.size > 16777216) throw new Error("Use lines() for files above 16 MiB.");
    const decoder = new TextDecoder(); let out = "";
    for await (const part of chunks(path)) out += decoder.decode(part, { stream: true });
    return out + decoder.decode();
  }
  async function* lines(path) {
    const decoder = new TextDecoder(); let tail = "";
    for await (const part of chunks(path)) {
      tail += decoder.decode(part, { stream: true }); let i;
      while ((i = tail.indexOf("\n")) >= 0) { yield tail.slice(0, i); tail = tail.slice(i + 1); }
      if (tail.length > 16777216) throw new Error("JSONL record exceeds 16 MiB.");
    }
    tail += decoder.decode(); if (tail) throw new Error("Archive has an incomplete final JSONL record.");
  }
  async function openLocalFiles() {
    const input = document.createElement("input"); input.type = "file"; input.multiple = true; input.webkitdirectory = true;
    return new Promise((resolve, reject) => {
      input.oncancel = () => reject(new Error("No archive selected."));
      input.onchange = async () => {
        try {
          const all = [...input.files], mf = all.find((f) => f.name === "manifest.json"); if (!mf) throw new Error("Choose the extracted archive directory containing manifest.json.");
          if (mf.size > 1048576) throw new Error("Archive manifest is too large.");
          const prefix = mf.webkitRelativePath.slice(0, -"manifest.json".length); manifest = JSON.parse(await mf.text());
          if (manifest.format !== "finalechat.website/v1" || !Array.isArray(manifest.files)) throw new Error("Unsupported archive format.");
          localFiles = new Map(all.filter((f) => f.webkitRelativePath.startsWith(prefix)).map((f) => [f.webkitRelativePath.slice(prefix.length), f])); verified = new Set();
          for (const f of manifest.files) { const local = localFiles.get(f.path); if (!local || local.size !== f.size) throw new Error("Missing or incomplete archive file: " + f.path); }
          readyResolve(false); emit("local", manifest); resolve(manifest);
        } catch (e) { reject(e); }
      };
      input.click();
    });
  }
  window.finale = Object.freeze({ version: 1, ready, get connected() { return connected; }, on, manifest: getManifest, read, text, lines, chunks, openLocalFiles,
    settings: Object.freeze({ read: () => rpc("settings.read"), propose: (proposal) => rpc("settings.propose", proposal), clear: () => rpc("settings.propose", null), onResult: (fn) => on("settings.result", fn) }),
    reveal: (anchor) => rpc("thread.reveal", anchor),
  });
})();
