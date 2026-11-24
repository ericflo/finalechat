import { useCallback, useEffect, useRef, useState } from "react";
import type { Attachment } from "../lib/types";
import { IconBack, IconClose, IconDownload, IconFile } from "./Icons";

export function humanSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

const maxTiles = 4;

/** What the viewer shows for an image: its compressed display copy when it
 * has one. The original stays behind `url`, for the download button. */
const viewSrc = (a: Attachment) => a.display_url ?? a.url;

// Images fetched ahead of need. Holding the elements keeps each request
// alive and its bytes in memory; the service worker stores them as well.
const warmed = new Map<string, HTMLImageElement>();

/** Starts loading an image's viewer copy before it is asked for. Only the
 * viewer does this: a press on a thumbnail is as often the start of a scroll
 * as a tap, and would spend data on images nobody opens. */
function warm(a: Attachment | undefined) {
  if (!a) return;
  const src = viewSrc(a);
  if (warmed.has(src)) return;
  const img = new Image();
  img.decoding = "async";
  img.src = src;
  warmed.set(src, img);
  if (warmed.size > 12) warmed.delete(warmed.keys().next().value as string);
}

/** Renders a message's attachments: an image grid plus file chips. */
export function AttachmentList({ items, onOpen }: { items: Attachment[]; onOpen: (images: Attachment[], index: number) => void }) {
  const images = items.filter((a) => a.kind === "image");
  const files = items.filter((a) => a.kind !== "image");
  const shown = images.length > maxTiles ? images.slice(0, maxTiles) : images;
  const extra = images.length - shown.length;
  return (
    <>
      {images.length > 0 && (
        <div className={`att-grid ${images.length === 1 ? "single" : ""}`}>
          {shown.map((a, i) => {
            const w = a.thumb_width || a.width || 4;
            const h = a.thumb_height || a.height || 3;
            const last = i === shown.length - 1 && extra > 0;
            return (
              <button
                key={a.id}
                type="button"
                className="att-image"
                style={images.length === 1 ? { aspectRatio: `${w} / ${h}` } : undefined}
                onClick={() => onOpen(images, i)}
                aria-label={last ? `Open ${a.filename} and ${extra} more` : `Open ${a.filename}`}
              >
                <img src={a.thumb_url ?? a.url} alt={a.filename} loading="lazy" decoding="async" />
                {last && <span className="att-more">+{extra}</span>}
              </button>
            );
          })}
        </div>
      )}
      {files.map((a) => (
        <a key={a.id} className="att-file" href={a.url} target="_blank" rel="noopener" download={a.filename}>
          <IconFile />
          <span className="att-name">{a.filename}</span>
          <span className="att-meta">{humanSize(a.size)}</span>
          <IconDownload className="att-dl" />
        </a>
      ))}
    </>
  );
}

interface View {
  scale: number;
  x: number;
  y: number;
}

const atRest: View = { scale: 1, x: 0, y: 0 };

/** One image in the viewer. The current one and its neighbours are all
 * mounted, so a neighbour has loaded and decoded by the time it is swiped
 * to. Until an image arrives its thumbnail (already on screen in the thread)
 * stands in at the same size. `load` holds a neighbour back until the image
 * on screen has arrived, so fetching ahead never slows the one being looked
 * at; once started, a load is not withdrawn. */
function Slide({ item, current, load, from, view, moving, onSettled }: { item: Attachment; current: boolean; load: boolean; from: number; view: View; moving: boolean; onSettled: (id: string) => void }) {
  const [src, setSrc] = useState(() => viewSrc(item));
  const [state, setState] = useState<"loading" | "loaded" | "failed">("loading");
  const [started, setStarted] = useState(load);
  if (load && !started) setStarted(true);
  const ref = useRef<HTMLImageElement>(null);
  const done = () => {
    setState("loaded");
    onSettled(item.id);
    // Decode now, so that showing it later is a paint and not a decode.
    void ref.current?.decode?.().catch(() => {});
  };
  useEffect(() => {
    // An image already in memory can finish before the handler is attached.
    if (ref.current?.complete && ref.current.naturalWidth > 0) done();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [src, started]);
  const style = {
    transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})`,
    transformOrigin: "center",
    transition: moving ? "none" : "transform 0.15s var(--ease)",
  };
  const w = item.display_width || item.width;
  const h = item.display_height || item.height;
  return (
    <div className={`viewer-slide ${current ? "current" : ""} ${current && from > 0 ? "from-next" : ""} ${current && from < 0 ? "from-prev" : ""}`} aria-hidden={!current}>
      {state === "loading" && item.thumb_url && w && h ? <img className="viewer-thumb" src={item.thumb_url} width={w} height={h} alt="" draggable={false} style={style} /> : null}
      {state === "failed" ? (
        <span className="viewer-failed">Could not load this image.</span>
      ) : (
        <img
          ref={ref}
          src={started ? src : undefined}
          alt={item.filename}
          draggable={false}
          onLoad={done}
          // The display copy can be missing; the original always exists.
          onError={() => {
            if (src !== item.url) return setSrc(item.url);
            setState("failed");
            onSettled(item.id);
          }}
          style={{ ...style, opacity: state === "loaded" ? 1 : 0 }}
        />
      )}
      {current && state === "loading" && (
        <span className="viewer-loading">
          <span className="spinner" />
        </span>
      )}
    </div>
  );
}

/** Full-screen image viewer with pinch, double-tap and wheel zoom, drag to pan,
 * and a swipe down to dismiss. Terminal screenshots need the zoom. */
export function ImageViewer({ items, index, onClose }: { items: Attachment[]; index: number; onClose: () => void }) {
  const [i, setI] = useState(Math.min(index, items.length - 1));
  const item = items[i] as Attachment;
  // Which way the last step went: the image arrives from that side, and the
  // one after it in that direction is fetched ahead.
  const [from, setFrom] = useState(0);
  // Images that have arrived (or given up).
  const [settled, setSettled] = useState<ReadonlySet<string>>(() => new Set());
  const onSettled = useCallback((id: string) => setSettled((s) => (s.has(id) ? s : new Set(s).add(id))), []);
  const [view, setView] = useState<View>(atRest);
  const go = (delta: number) => {
    const next = i + delta;
    if (next < 0 || next >= items.length) return;
    setI(next);
    setFrom(delta);
    setView(atRest);
  };
  // Fetching ahead goes in order of need: the image on screen, then its
  // neighbours (mounted below), then one further along the way being swiped.
  const here = settled.has(item.id);
  const ahead = from < 0 ? -1 : 1;
  const neighbour = items[i + ahead];
  const further = here && (!neighbour || settled.has(neighbour.id)) ? items[i + 2 * ahead] : undefined;
  useEffect(() => warm(further), [further]);
  const bodyRef = useRef<HTMLDivElement>(null);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ start: View; dist: number; cx: number; cy: number; moved: boolean } | null>(null);
  const lastTap = useRef(0);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowRight") go(1);
      if (e.key === "ArrowLeft") go(-1);
    };
    window.addEventListener("keydown", onKey);
    const { overflow } = document.body.style;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onClose, i]);

  const clamp = (v: View): View => {
    const scale = Math.min(5, Math.max(1, v.scale));
    if (scale === 1) return { scale: 1, x: 0, y: 0 };
    const el = bodyRef.current;
    const w = el?.clientWidth ?? 0;
    const h = el?.clientHeight ?? 0;
    // Keep the image covering the viewport while zoomed.
    const maxX = (w * (scale - 1)) / 2;
    const maxY = (h * (scale - 1)) / 2;
    return { scale, x: Math.min(maxX, Math.max(-maxX, v.x)), y: Math.min(maxY, Math.max(-maxY, v.y)) };
  };

  const zoomAt = (factor: number, cx: number, cy: number, base: View = view) => {
    const el = bodyRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    // Zoom about the pointer: keep the point under the finger fixed.
    const px = cx - rect.left - rect.width / 2;
    const py = cy - rect.top - rect.height / 2;
    const next = clamp({ scale: base.scale * factor, x: base.x, y: base.y });
    const ratio = next.scale / base.scale;
    setView(clamp({ scale: next.scale, x: px - (px - base.x) * ratio, y: py - (py - base.y) * ratio }));
  };

  const onPointerDown = (e: React.PointerEvent) => {
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const pts = Array.from(pointers.current.values());
    if (pts.length === 2) {
      const [a, b] = pts as [{ x: number; y: number }, { x: number; y: number }];
      gesture.current = { start: view, dist: Math.hypot(a.x - b.x, a.y - b.y), cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2, moved: true };
    } else if (pts.length === 1) {
      gesture.current = { start: view, dist: 0, cx: e.clientX, cy: e.clientY, moved: false };
    }
  };

  const onPointerMove = (e: React.PointerEvent) => {
    if (!pointers.current.has(e.pointerId) || !gesture.current) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const pts = Array.from(pointers.current.values());
    const g = gesture.current;
    if (pts.length >= 2) {
      const [a, b] = pts as [{ x: number; y: number }, { x: number; y: number }];
      const dist = Math.hypot(a.x - b.x, a.y - b.y);
      if (g.dist > 0) zoomAt(dist / g.dist, (a.x + b.x) / 2, (a.y + b.y) / 2, g.start);
      g.moved = true;
      return;
    }
    const dx = e.clientX - g.cx;
    const dy = e.clientY - g.cy;
    if (Math.abs(dx) + Math.abs(dy) > 6) g.moved = true;
    if (g.start.scale > 1) setView(clamp({ scale: g.start.scale, x: g.start.x + dx, y: g.start.y + dy }));
    else if (dy > 0) setView({ scale: 1, x: 0, y: dy * 0.6 });
  };

  const onPointerUp = (e: React.PointerEvent) => {
    pointers.current.delete(e.pointerId);
    const g = gesture.current;
    if (!g) return;
    if (pointers.current.size > 0) {
      // One finger left: continue as a pan from here.
      const rest = Array.from(pointers.current.values())[0] as { x: number; y: number };
      gesture.current = { start: view, dist: 0, cx: rest.x, cy: rest.y, moved: true };
      return;
    }
    gesture.current = null;
    if (g.start.scale === 1 && view.y > 90) {
      onClose();
      return;
    }
    // At rest, a horizontal swipe pages through the message's images.
    const dx = e.clientX - g.cx;
    const dy = e.clientY - g.cy;
    if (g.start.scale === 1 && view.scale === 1 && Math.abs(dx) > 60 && Math.abs(dy) < 60) {
      setView({ scale: 1, x: 0, y: 0 });
      go(dx < 0 ? 1 : -1);
      return;
    }
    if (g.start.scale === 1 && view.scale === 1) setView({ scale: 1, x: 0, y: 0 });
    if (!g.moved) {
      // Single tap on the backdrop (outside the image) dismisses the viewer.
      const t = e.target as HTMLElement | null;
      if (t && t.tagName !== "IMG" && !t.closest?.("img")) {
        onClose();
        return;
      }
      const now = Date.now();
      if (now - lastTap.current < 320) {
        // Double tap: zoom in around the tap, or back out.
        if (view.scale > 1) setView({ scale: 1, x: 0, y: 0 });
        else zoomAt(2.5, e.clientX, e.clientY);
        lastTap.current = 0;
      } else {
        lastTap.current = now;
      }
    }
  };

  const onWheel = (e: React.WheelEvent) => {
    e.preventDefault();
    zoomAt(e.deltaY < 0 ? 1.15 : 1 / 1.15, e.clientX, e.clientY);
  };

  return (
    <div className="viewer" role="dialog" aria-modal="true" aria-label={item.filename}>
      <div className="viewer-bar">
        <span className="viewer-title">
          {item.filename}
          <span className="att-meta">
            {" "}
            · {item.width}×{item.height} · {humanSize(item.size)}
            {items.length > 1 && ` · ${i + 1} / ${items.length}`}
          </span>
        </span>
        {items.length > 1 && (
          <>
            <button type="button" className="icon-btn" onClick={() => go(-1)} disabled={i === 0} aria-label="Previous image">
              <IconBack />
            </button>
            <button type="button" className="icon-btn viewer-next" onClick={() => go(1)} disabled={i >= items.length - 1} aria-label="Next image">
              <IconBack />
            </button>
          </>
        )}
        <a className="icon-btn" href={item.url} download={item.filename} target="_blank" rel="noopener" aria-label="Download">
          <IconDownload />
        </a>
        <button type="button" className="icon-btn" onClick={onClose} aria-label="Close">
          <IconClose />
        </button>
      </div>
      <div ref={bodyRef} className="viewer-body" onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={onPointerUp} onWheel={onWheel}>
        {[i - 1, i, i + 1].map((n) => {
          const it = items[n];
          return it ? <Slide key={it.id} item={it} current={n === i} load={n === i || here} from={from} view={n === i ? view : atRest} moving={n === i && gesture.current !== null} onSettled={onSettled} /> : null;
        })}
      </div>
      {here && view.scale === 1 && <div className="viewer-hint">{items.length > 1 ? "Swipe for the next image · " : ""}Double-tap or pinch to zoom · swipe down to close</div>}
    </div>
  );
}

/** Pending uploads shown above the composer. */
export interface PendingUpload {
  key: string;
  file: File;
  previewURL: string | null;
  progress: number;
  attachment: Attachment | null;
  error: string | null;
}

export function UploadTray({ items, onRemove }: { items: PendingUpload[]; onRemove: (key: string) => void }) {
  if (items.length === 0) return null;
  return (
    <div className="upload-tray">
      {items.map((u) => (
        <div key={u.key} className={`upload-item ${u.error ? "error" : ""} ${u.previewURL ? "image" : ""}`} title={u.error ?? u.file.name}>
          {u.previewURL ? <img src={u.previewURL} alt="" /> : <IconFile />}
          {!u.attachment && !u.error && <div className="upload-progress" style={{ width: `${Math.round(u.progress * 100)}%` }} />}
          <button type="button" className="upload-remove" aria-label={`Remove ${u.file.name}`} onClick={() => onRemove(u.key)}>
            <IconClose />
          </button>
          <span className="upload-name">{u.error ? "Failed" : u.file.name}</span>
        </div>
      ))}
    </div>
  );
}
