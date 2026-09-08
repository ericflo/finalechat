import { useEffect, useRef, useState } from "react";
import { artifactAPI } from "../lib/artifacts";
import { sourceForMessage } from "../lib/source-anchor";
import { navigate } from "../lib/router";
import { toast } from "../lib/store";
import type { Message, Thread } from "../lib/types";

export function InspectMessage({ message, thread }: { message: Message; thread: Thread }) {
  const [busy, setBusy] = useState(false);
  const [choices, setChoices] = useState<{ title: string; url: string }[]>([]);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const inspect = async () => {
    setBusy(true); setChoices([]);
    try {
      const items = (await artifactAPI.list(thread.id)).artifacts.filter(a => a.current_revision_id).slice(0, 50);
      const found: { title: string; url: string }[] = [];
      for (let start = 0; start < items.length; start += 5) {
        const results = await Promise.allSettled(items.slice(start, start + 5).map(a => artifactAPI.get(a.id)));
        if (!alive.current) return;
        for (const result of results) {
          if (result.status !== "fulfilled") continue;
          const { artifact, revision } = result.value;
          if (!revision || artifact.thread_id !== thread.id) continue;
          const anchor = sourceForMessage(message, thread, revision.manifest);
          if (anchor) found.push({ title: artifact.title, url: `/t/${thread.id}/artifacts/${artifact.id}?${new URLSearchParams({ revision: revision.id, anchor: JSON.stringify(anchor) })}` });
        }
      }
      if (!alive.current) return;
      if (found.length === 1) navigate(found[0]!.url);
      else if (found.length) setChoices(found);
      else toast("No matching session archive has been published yet.");
    } catch { if (alive.current) toast("Could not open the saved session.", "error"); }
    finally { if (alive.current) setBusy(false); }
  };
  return <div className="inspect-message"><button className="btn small" disabled={busy} onClick={() => void inspect()}>{busy ? "Finding saved event…" : "Inspect this moment"}</button>{choices.map(c => <button key={c.url} className="btn small" onClick={() => navigate(c.url)}>{c.title}</button>)}</div>;
}
