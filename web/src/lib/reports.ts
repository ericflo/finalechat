// A relayed agent's report too long for one post arrives as several posts,
// each with meta `part` (from 1) and `parts`, and a closing line
// "_(part i of n)_" so the text says it on clients that do not read meta.
// Pure functions, tested in web/tests/reports.mjs.

export type ReportPart = { part: number; parts: number };

/** Which part of a longer report a message is, or null for a whole one. */
export function reportPart(meta: Record<string, unknown> | null | undefined): ReportPart | null {
  const part = meta?.part;
  const parts = meta?.parts;
  if (typeof part !== "number" || typeof parts !== "number") return null;
  if (!Number.isInteger(part) || !Number.isInteger(parts) || parts < 2 || part < 1 || part > parts) return null;
  return { part, parts };
}

/** The report's own text: the body without a closing marker line for this
 * part's numbers, which the card already shows. Anything else is kept. */
export function reportText(body: string, p: ReportPart | null): string {
  if (!p) return body;
  const trimmed = body.replace(/\s+$/, "");
  const cut = trimmed.lastIndexOf("\n");
  const last = trimmed.slice(cut + 1).trim();
  if (cut < 0 || last !== `_(part ${p.part} of ${p.parts})_`) return body;
  return trimmed.slice(0, cut).replace(/\s+$/, "");
}
