import { useEffect, useRef, useState } from "react";
import { answerQuestion, toast, useStore } from "../lib/store";
import type { Question } from "../lib/types";
import { fullDateTime, relativeTime } from "../lib/time";
import { IconCheck } from "./Icons";
import { Link } from "./Common";

export function QuestionCard({ q, showThread, highlight }: { q: Question; showThread?: boolean; highlight?: boolean }) {
  const thread = useStore((s) => s.threads[q.thread_id]);
  const [selected, setSelected] = useState<string[]>([]);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const pending = q.status === "pending";

  useEffect(() => {
    if (highlight && ref.current) {
      ref.current.scrollIntoView({ block: "center", behavior: "smooth" });
    }
  }, [highlight]);

  const toggle = (label: string) => {
    if (!pending) return;
    setSelected((cur) => {
      if (q.multi_select) return cur.includes(label) ? cur.filter((l) => l !== label) : [...cur, label];
      return cur.includes(label) ? [] : [label];
    });
  };

  const canSubmit = pending && !busy && (selected.length > 0 || text.trim().length > 0);

  const submit = async () => {
    if (!canSubmit) return;
    setBusy(true);
    try {
      await answerQuestion(q.id, { selected, text: text.trim() || undefined });
      if (navigator.vibrate) navigator.vibrate(12);
    } catch (err) {
      toast(err instanceof Error ? err.message : "Could not send the answer.", "error");
    } finally {
      setBusy(false);
    }
  };

  const statusLabel = { pending: "Needs you", answered: "Answered", cancelled: "Withdrawn", expired: "Expired" }[q.status];

  return (
    <div ref={ref} className={`question ${pending ? "" : "resolved"}`} data-question-id={q.id}>
      <div className="q-head">
        <span>{statusLabel}</span>
        <span style={{ fontWeight: 500, letterSpacing: 0, textTransform: "none", color: "var(--text-3)" }} title={fullDateTime(q.created_at)}>
          {relativeTime(q.created_at)}
        </span>
        {showThread && thread && (
          <Link href={`/t/${thread.id}?q=${q.id}`} className="q-thread">
            {thread.title || thread.agent || "Thread"}
          </Link>
        )}
      </div>
      <div className="q-prompt">{q.prompt}</div>

      {pending && (
        <>
          {q.options.length > 0 && (
            <div className="q-options" role={q.multi_select ? "group" : "radiogroup"}>
              {q.options.map((o) => {
                const on = selected.includes(o.label);
                return (
                  <button
                    key={o.label}
                    type="button"
                    className={`q-option ${on ? "selected" : ""}`}
                    role={q.multi_select ? "checkbox" : "radio"}
                    aria-checked={on}
                    disabled={busy}
                    onClick={() => toggle(o.label)}
                  >
                    <span className={`radio ${q.multi_select ? "square" : ""}`}>{on && <IconCheck />}</span>
                    <span>
                      <div className="o-label">{o.label}</div>
                      {o.description && <div className="o-desc">{o.description}</div>}
                    </span>
                  </button>
                );
              })}
            </div>
          )}
          {q.allow_freeform && (
            <div className="q-free">
              <textarea
                placeholder={q.options.length > 0 ? "Or write your own answer…" : "Write your answer…"}
                value={text}
                rows={1}
                onChange={(e) => {
                  setText(e.target.value);
                  e.target.style.height = "auto";
                  e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`;
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void submit();
                }}
              />
            </div>
          )}
          <div className="q-actions">
            {q.expires_at && <span className="q-expires">Expires {relativeTime(q.expires_at)}</span>}
            <span className="spacer" />
            <button type="button" className="btn primary small" disabled={!canSubmit} onClick={submit}>
              {busy ? "Sending…" : selected.length > 0 && !q.multi_select && !text.trim() ? `Send · ${selected[0]}` : "Send answer"}
            </button>
          </div>
        </>
      )}

      {q.status === "answered" && q.answer && (
        <div className="q-answer">
          <div className="who">You answered</div>
          {q.answer.selected.length > 0 && <div style={{ fontWeight: 600 }}>{q.answer.selected.join(", ")}</div>}
          {q.answer.text && <div style={{ whiteSpace: "pre-wrap" }}>{q.answer.text}</div>}
        </div>
      )}
      {(q.status === "cancelled" || q.status === "expired") && (
        <div className="q-answer gone">{q.status === "cancelled" ? "The agent withdrew this question." : "This question expired before it was answered."}</div>
      )}
    </div>
  );
}
