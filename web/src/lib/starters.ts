/** Minimal starter shape needed to display and choose between connected hosts. */
export interface StarterLike {
  resource: { id: string; label: string };
  connector: { id: string; name: string; provider: string };
  online: boolean;
}

const knownProvider = (p: string) =>
  p === "claude-code" ? "Claude Code" : p === "codex" ? "Codex" : p === "antigravity" ? "Antigravity" : p;

/** Extracts the host/machine qualifier from a connector name such as
 * "eagent · llmbox · Development" by dropping a leading provider name and a
 * trailing project label. Returns "" when nothing distinguishes it further. */
export function starterQualifier(connectorName: string, provider: string, resourceLabel: string): string {
  const parts = connectorName
    .split("·")
    .map((p) => p.trim())
    .filter(Boolean);
  const prov = provider.trim().toLowerCase();
  const provLabel = knownProvider(provider.trim()).toLowerCase();
  if (parts.length > 0 && (parts[0]!.toLowerCase() === prov || parts[0]!.toLowerCase() === provLabel)) {
    parts.shift();
  }
  const label = resourceLabel.trim().toLowerCase();
  if (parts.length > 0 && label !== "" && parts[parts.length - 1]!.toLowerCase() === label) {
    parts.pop();
  }
  return parts.join(" · ");
}

/** Short label for a starter in pickers: "llmbox · Development" when a host
 * qualifier is present, otherwise the project label (or connector name). */
export function starterDisplayName(s: StarterLike): string {
  const label = s.resource.label.trim();
  const qual = starterQualifier(s.connector.name, s.connector.provider, label);
  if (qual && label) return `${qual} · ${label}`;
  return label || qual || s.connector.name.trim();
}

/** Picks which starter /new opens on: the remembered one if it is online, else
 * the first online starter, else the remembered one, else the first starter. */
export function pickDefaultStarter<T extends StarterLike>(starters: readonly T[], rememberedId: string | null): T | undefined {
  if (rememberedId) {
    const rememberedOnline = starters.find((s) => s.resource.id === rememberedId && s.online);
    if (rememberedOnline) return rememberedOnline;
  }
  const firstOnline = starters.find((s) => s.online);
  if (firstOnline) return firstOnline;
  if (rememberedId) {
    const remembered = starters.find((s) => s.resource.id === rememberedId);
    if (remembered) return remembered;
  }
  return starters[0];
}

/** Starters offered in the /new picker: every online starter, plus the
 * currently selected starter if it happens to be offline. Online starters
 * come first while keeping relative order within each group. */
export function pickableStarters<T extends StarterLike>(starters: readonly T[], currentId: string | null): T[] {
  const online = starters.filter((s) => s.online);
  const currentOffline = currentId ? starters.filter((s) => !s.online && s.resource.id === currentId) : [];
  return [...online, ...currentOffline];
}
