import { useEffect, useState } from "react";
import { TopBar } from "../components/TopBar";
import { renderMarkdown } from "../lib/markdown";

export function DocsScreen({ path, title }: { path: string; title: string }) {
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setHtml(null);
    fetch(path, { headers: { Accept: "text/markdown" } })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((md) => setHtml(renderMarkdown(md)))
      .catch((e) => setError(e.message));
  }, [path]);
  return (
    <div className="page">
      <TopBar title={title} backTo="/" />
      <div className="docs">
        {error && <div className="form-error">{error}</div>}
        {html === null && !error && <div className="skeleton" style={{ height: 300 }} />}
        {html !== null && <div className="md" dangerouslySetInnerHTML={{ __html: html }} />}
      </div>
    </div>
  );
}
