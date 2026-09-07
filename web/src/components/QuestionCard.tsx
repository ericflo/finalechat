import { memo, useEffect, useRef, useState } from "react";
import { answerQuestion, dismissQuestion, toast, useStore } from "../lib/store";
import type { Question } from "../lib/types";
import { compactTime, countdown, fullDateTime, useNow } from "../lib/time";
import { IconCheck } from "./Icons";
import { Avatar, Link } from "./Common";

/** Options shown before an expander; a phone question is a 2-5 way choice. */
const maxOptions = 5;

export const QuestionCard = memo(function QuestionCard({ q, showThread, highlight }: { q: Question; showThread?: boolean; highlight?: boolean }) {
  const thread = useStore((s) => s.threads[q.thread_id]);
  const [selected, setSelected] = useState<string[]>([]);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmDismiss, setConfirmDismiss] = useState(false);
  const [allOptions, setAllOptions] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const expiresAt = q.expires_at ? new Date(q.expires_at).getTime() : 0;
  // Tick every second in the last ten minutes, every half minute otherwise.
  const now = useNow(q.status === "pending" && expiresAt && expiresAt - Date.now() < 10 * 60 * 1000 ? 1000 : 30000);
  const remaining = expiresAt ? expiresAt - now : Infinity;
  // The server sweeps expiries every few seconds; the card flips at zero on its own.
  const pending = q.status === "pending" && remaining > 0;
  const localExpired = q.status === "pending" && remaining <= 0;

  useEffect(() => {
    if (!highlight || !ref.current) return;
    const el = ref.current;
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    // Images and markdown above may still be laying out; anchor again once they settle.
    const t = window.setTimeout(() => el.scrollIntoView({ block: "center", behavior: "smooth" }), 450);
    return () => window.clearTimeout(t);
  }, [highlight]);

  useEffect(() => {
    if (!confirmDismiss) return;
    const t = window.setTimeout(() => setConfirmDismiss(false), 6000);
    return () => window.clearTimeout(t);
  }, [confirmDismiss]);

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

  const dismiss = async () => {
    setConfirmDismiss(false);
    setBusy(true);
    try {
      await dismissQuestion(q.id);
      toast("Dismissed. The agent will carry on without an answer.");
    } catch (err) {
      toast(err instanceof Error ? err.message : "Could not dismiss.", "error");
    } finally {
      setBusy(false);
    }
  };

  const status = localExpired ? "expired" : q.status;
  const statusLabel = { pending: "Needs you", answered: "Answered", cancelled: "Withdrawn", expired: "Expired", dismissed: "Dismissed" }[status];
  const header = typeof q.meta.header === "string" && q.meta.header.trim() ? q.meta.header.trim().slice(0, 48) : "";
  const agentName = thread?.agent || thread?.title || "Agent";
  const urgent = pending && remaining < 2 * 60 * 1000;

  return (
    <div ref={ref} className={`question ${pending ? "" : "resolved"} ${highlight ? "highlight" : ""}`} data-question-id={q.id}>
      <div className="q-head">
        {showThread && thread && (
          <Link href={`/t/${thread.id}?q=${q.id}`} className="q-agent" aria-label={`Open thread ${thread.title || thread.agent}`}>
            <Avatar name={agentName} small />
            <span className="q-agent-name">
              {thread.title || thread.agent || "Thread"}
              {thread.title && thread.agent && <span className="q-agent-sub"> · {thread.agent}</span>}
            </span>
          </Link>
        )}
        {(!showThread || !pending || header) && <span className={`q-eyebrow ${pending ? "" : "muted"}`}>{pending && header ? header : statusLabel}</span>}
        <span className="q-time" title={fullDateTime(q.created_at)}>
          {compactTime(q.created_at, now)}
        </span>
      </div>
      <div className="q-prompt">{q.prompt}</div>

      {pending && (
        <>
          {q.options.length > 0 && (
            <div className="q-options" role={q.multi_select ? "group" : "radiogroup"}>
              {(allOptions || q.options.length <= maxOptions ? q.options : q.options.slice(0, maxOptions)).map((o) => {
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
              {!allOptions && q.options.length > maxOptions && (
                <button type="button" className="btn small" onClick={() => setAllOptions(true)}>
                  +{q.options.length - maxOptions} more option{q.options.length - maxOptions === 1 ? "" : "s"}
                </button>
              )}
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
            <button type="button" className="btn primary q-send" disabled={!canSubmit} onClick={submit}>
              {busy ? "Sending…" : selected.length > 0 && !q.multi_select && !text.trim() ? `Send · ${selected[0]}` : "Send answer"}
            </button>
            <div className="q-foot">
              {q.expires_at && (
                <span className={`q-expires ${urgent ? "urgent" : ""}`} title={fullDateTime(q.expires_at)}>
                  Expires in {countdown(remaining)}
                </span>
              )}
              <span className="spacer" />
              {!confirmDismiss && (
                <button type="button" className="btn ghost small" disabled={busy} onClick={() => setConfirmDismiss(true)}>
                  Dismiss
                </button>
              )}
            </div>
            {confirmDismiss && (
              <div className="q-confirm" role="group" aria-label="Confirm dismissal">
                <span>Dismiss without answering? The agent carries on without you.</span>
                <div className="q-confirm-actions">
                  <button type="button" className="btn small" onClick={() => setConfirmDismiss(false)}>
                    Keep
                  </button>
                  <button type="button" className="btn small danger" disabled={busy} onClick={dismiss}>
                    Dismiss
                  </button>
                </div>
              </div>
            )}
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
      {(status === "cancelled" || status === "expired" || status === "dismissed") && (
        <div className="q-answer gone">
          {status === "cancelled" ? "The agent withdrew this question." : status === "expired" ? "This question expired before it was answered." : "You dismissed this question."}
        </div>
      )}
    </div>
  );
});
