import DOMPurify from "dompurify";
import { marked } from "marked";

marked.setOptions({ gfm: true, breaks: true });

DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A") {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer nofollow");
  }
});

const cache = new Map<string, string>();

export function renderMarkdown(source: string): string {
  const hit = cache.get(source);
  if (hit !== undefined) return hit;
  let html: string;
  try {
    html = marked.parse(source, { async: false }) as string;
  } catch {
    html = escapeHTML(source);
  }
  const clean = DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ["style", "form", "input", "button", "iframe", "object", "embed"],
    ADD_ATTR: ["target"],
  });
  if (cache.size > 500) cache.clear();
  cache.set(source, clean);
  return clean;
}

export function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c] as string);
}

export function renderText(source: string): string {
  // Plain text with clickable links and preserved line breaks.
  const escaped = escapeHTML(source);
  const linked = escaped.replace(/https?:\/\/[^\s<]+/g, (url) => `<a href="${url}" target="_blank" rel="noopener noreferrer nofollow">${url}</a>`);
  return linked.replace(/\n/g, "<br>");
}
