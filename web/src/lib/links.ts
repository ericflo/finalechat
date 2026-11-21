// Bare URLs in plain text (question prompts, answers, thread summaries,
// plain-text messages) become links. Only http(s): nothing else is ever a
// link target.

export type LinkSegment = string | { url: string };

// Quotes, angle brackets and backticks never belong to a bare URL, and
// neither does typographic punctuation an agent puts right after one.
const URL_RE = /\bhttps?:\/\/[^\s<>"'`–—…“”‘’]+/gi;
const CLOSERS: Record<string, string> = { ")": "(", "]": "[", "}": "{" };

const count = (s: string, ch: string) => s.split(ch).length - 1;

/** Drops sentence punctuation and emphasis after a URL ("see https://x.dev/."),
 * and a closing bracket the URL itself did not open. */
function trimURL(url: string): string {
  for (;;) {
    const last = url[url.length - 1] ?? "";
    if (/[.,;:!?*_~]/.test(last)) {
      url = url.slice(0, -1);
      continue;
    }
    const open = CLOSERS[last];
    if (open && count(url, last) > count(url, open)) {
      url = url.slice(0, -1);
      continue;
    }
    return url;
  }
}

/** Splits text into plain runs and URLs, in order. */
export function linkSegments(text: string): LinkSegment[] {
  const out: LinkSegment[] = [];
  let at = 0;
  for (const m of text.matchAll(URL_RE)) {
    const url = trimURL(m[0]);
    if (!/^https?:\/\/[^/?#]/i.test(url)) continue;
    const start = m.index ?? 0;
    if (start > at) out.push(text.slice(at, start));
    out.push({ url });
    at = start + url.length;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}
