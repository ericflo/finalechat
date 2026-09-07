import DOMPurify from "dompurify";
import { marked } from "marked";

marked.setOptions({ gfm: true, breaks: true });

DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A") {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer nofollow");
  }
});

// Terminal output pasted by agents often carries colour codes; they are
// noise on a phone.
const ANSI = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(?:\x07|\x1b\\)/g;

export function stripANSI(s: string): string {
  return s.includes("\x1b") ? s.replace(ANSI, "") : s;
}

const cache = new Map<string, string>();
const cacheLimit = 400;

function remember(key: string, value: string): string {
  if (cache.size >= cacheLimit) {
    // Drop the oldest entries rather than everything at once.
    let n = 0;
    for (const k of cache.keys()) {
      cache.delete(k);
      if (++n >= 50) break;
    }
  }
  cache.set(key, value);
  return value;
}

/** Wraps each code block so a copy button can sit outside the scroller. */
function decorateCode(html: string): string {
  if (!html.includes("<pre>")) return html;
  return html
    .replace(/<pre>(?:<code(?: class="language-([\w+.#-]+)")?>)?/g, (m, lang: string | undefined) => {
      const chip = lang ? `<span class="code-lang">${escapeHTML(lang)}</span>` : "<span></span>";
      return `<div class="code-block"><div class="code-head">${chip}<button type="button" class="code-copy" aria-label="Copy code">Copy</button></div>${m}`;
    })
    .replace(/<\/pre>/g, "</pre></div>");
}

export function renderMarkdown(source: string): string {
  const hit = cache.get(source);
  if (hit !== undefined) return hit;
  let html: string;
  try {
    html = marked.parse(stripANSI(source), { async: false }) as string;
  } catch {
    html = escapeHTML(source);
  }
  const clean = DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ["style", "form", "input", "button", "iframe", "object", "embed"],
    ADD_ATTR: ["target"],
  });
  return remember(source, decorateCode(clean));
}

export function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c] as string);
}

export function renderText(source: string): string {
  // Plain text with clickable links and preserved line breaks.
  const escaped = escapeHTML(stripANSI(source));
  const linked = escaped.replace(/https?:\/\/[^\s<]+/g, (url) => `<a href="${url}" target="_blank" rel="noopener noreferrer nofollow">${url}</a>`);
  return linked.replace(/\n/g, "<br>");
}
