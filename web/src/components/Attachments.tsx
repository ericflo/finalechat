import { memo, useCallback, useEffect, useRef, useState } from "react";
import type { Attachment } from "../lib/types";
import { toast } from "../lib/store";
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
            // A long screenshot or a panorama shows its start: the server
            // makes its thumbnail of that, at no more than 2:5 or 3:1. A
            // thumbnail from before it did is held to the same shape.
            const shape = between(w / h, 2 / 5, 3);
            const last = i === shown.length - 1 && extra > 0;
            return (
              <button
                key={a.id}
                type="button"
                className="att-image"
                style={images.length === 1 ? { aspectRatio: `${shape}` } : undefined}
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

/** Space between neighbouring images in the strip, in pixels. */
const slideGap = 16;

/** How much of a pull past the first or last image the strip gives to. */
const edgeGive = 0.3;

const between = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/** The transition that brings the strip to rest `dist` pixels away, let go
 * of at `speed` px/ms towards it. The curve leaves at the speed the finger
 * had, so the hand-off has no jolt: a flick coasts in and slows, a strip
 * let go of at a standstill eases away and settles. */
function glideTo(dist: number, speed: number): string {
  if (dist < 1) return "";
  const ms = Math.round(speed > 0.1 ? between((2.5 * dist) / speed, 180, 360) : between(160 + dist * 0.45, 180, 360));
  // A cubic-bezier (x1, y1, …) sets off at y1/x1 times its average speed.
  const y1 = between((0.2 * speed * ms) / dist, 0, 1);
  return `transform ${ms}ms cubic-bezier(0.2, ${y1.toFixed(3)}, 0.3, 1)`;
}

/** One image in the viewer's strip, at position `at`. The current one and
 * its neighbours are all mounted, so a neighbour has loaded and decoded by
 * the time it is pulled into view. Until an image arrives its thumbnail
 * (already on screen in the thread) stands in at the same size. `load` holds
 * a neighbour back until the image on screen has arrived, so fetching ahead
 * never slows the one being looked at; once started, a load is not
 * withdrawn. Memoised: dragging the strip moves it without re-rendering the
 * images on it. */
const Slide = memo(function Slide({ item, at, current, load, view, moving, onSettled }: { item: Attachment; at: number; current: boolean; load: boolean; view: View; moving: boolean; onSettled: (id: string) => void }) {
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
    <div className="viewer-slide" style={{ transform: `translateX(calc(${at} * (100% + ${slideGap}px)))` }} aria-hidden={!current}>
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
});

interface Gesture {
  start: View;
  dist: number;
  cx: number;
  cy: number;
  moved: boolean;
  /** At rest one finger does one of two things, and whichever way it sets
   * off is the way it stays: sideways drags the strip, down pulls the image
   * away to close. */
  axis: "x" | "y" | null;
  /** Where the strip was when it was touched; not zero if it was caught
   * while still gliding from the last swipe. */
  base: number;
  /** Sideways speed in px/ms, and the sample it was last measured from. */
  vx: number;
  px: number;
  pt: number;
}

/** Full-screen image viewer. The images of a message sit side by side in a
 * strip: a sideways drag pulls the next one in under the finger, and on
 * release the strip glides to whichever image it should rest on. Pinch,
 * double-tap and wheel zoom, drag to pan, and a swipe down to dismiss.
 * Terminal screenshots need the zoom. */
export function ImageViewer({ items, index, onClose }: { items: Attachment[]; index: number; onClose: () => void }) {
  const [i, setI] = useState(Math.min(index, items.length - 1));
  const item = items[i] as Attachment;
  // The images mounted, first to last: the current one and its neighbours,
  // and for as long as the strip is still moving, any that a quick run of
  // swipes has left on their way out of view.
  const [span, setSpan] = useState<readonly [number, number]>(() => [i - 1, i + 1]);
  const tidy = () => setSpan((s) => (s[0] === i - 1 && s[1] === i + 1 ? s : [i - 1, i + 1]));
  // Which way the last step went: the image after the next in that direction
  // is fetched ahead.
  const [from, setFrom] = useState(0);
  // How far the strip is dragged from where image i rests, and the
  // transition that last sent it gliding ("" for the stylesheet's own).
  const [drag, setDrag] = useState(0);
  const [glide, setGlide] = useState("");
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
    setSpan(([lo, hi]) => [Math.min(lo, next - 1), Math.max(hi, next + 1)]);
  };
  // Fetching ahead goes in order of need: the image on screen, then its
  // neighbours (mounted below), then one further along the way being swiped.
  const here = settled.has(item.id);
  const ahead = from < 0 ? -1 : 1;
  const neighbour = items[i + ahead];
  const further = here && (!neighbour || settled.has(neighbour.id)) ? items[i + 2 * ahead] : undefined;
  useEffect(() => warm(further), [further]);
  const bodyRef = useRef<HTMLDivElement>(null);
  const trackRef = useRef<HTMLDivElement>(null);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<Gesture | null>(null);
  const lastTap = useRef(0);

  /** The distance from one image's resting place to the next. */
  const stride = () => (bodyRef.current?.clientWidth ?? 0) + slideGap;
  /** How far the strip is from rest right now, as drawn: not zero while it
   * is still gliding from the last swipe. */
  const strayed = () => {
    const el = trackRef.current;
    if (!el) return 0;
    const off = new DOMMatrixReadOnly(getComputedStyle(el).transform).m41 + i * stride();
    return Math.abs(off) < 1 ? 0 : off;
  };
  // Past the first or last image the strip gives a little and no more.
  const pastEnd = (x: number) => (x > 0 && i === 0) || (x < 0 && i === items.length - 1);
  const pull = (x: number) => (pastEnd(x) ? x * edgeGive : x);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
      setGlide("");
      go(e.key === "ArrowRight" ? 1 : -1);
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

  /** How image `item` sits in the viewer: `fill` is the zoom at which it is
   * as wide as the viewer (more than 1 only for an image much taller than
   * the screen, such as a long screenshot), and `most` how far it zooms: 5×,
   * or for a long or wide image far enough to fill the viewer and as much
   * again, though never past twice its own pixels. */
  const zoomRange = () => {
    const el = bodyRef.current;
    const iw = item.display_width || item.width || 0;
    const ih = item.display_height || item.height || 0;
    if (!el || !iw || !ih || !el.clientWidth || !el.clientHeight) return { fill: 1, most: 5 };
    // The image is fitted inside its slide, which keeps clear of the bottom.
    const slide = trackRef.current?.querySelector(".viewer-slide");
    const w = el.clientWidth;
    const h = el.clientHeight - (slide ? parseFloat(getComputedStyle(slide).paddingBottom) || 0 : 0);
    const fit = Math.min(1, w / iw, h / ih);
    const fill = w / (iw * fit);
    const cover = Math.max(fill, h / (ih * fit));
    return { fill: Math.min(fill, 1 / fit), most: Math.max(5, Math.min(2 * cover, 2 / fit)) };
  };

  const clamp = (v: View): View => {
    const scale = Math.min(zoomRange().most, Math.max(1, v.scale));
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
      gesture.current = { start: view, dist: Math.hypot(a.x - b.x, a.y - b.y), cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2, moved: true, axis: null, base: 0, vx: 0, px: 0, pt: 0 };
      setDrag(0); // a second finger turns a drag into a pinch
    } else if (pts.length === 1) {
      // A touch while the strip is still gliding catches it where it is.
      let base = strayed();
      if (!base) tidy();
      if (view.scale > 1) base = 0;
      else if (pastEnd(base)) base /= edgeGive;
      gesture.current = { start: view, dist: 0, cx: e.clientX, cy: e.clientY, moved: false, axis: base ? "x" : null, base, vx: 0, px: e.clientX, pt: e.timeStamp };
      if (base) setDrag(pull(base));
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
    let dx = e.clientX - g.cx;
    let dy = e.clientY - g.cy;
    if (Math.abs(dx) + Math.abs(dy) > 6) g.moved = true;
    if (g.start.scale > 1) {
      setView(clamp({ scale: g.start.scale, x: g.start.x + dx, y: g.start.y + dy }));
      return;
    }
    if (!g.axis && Math.abs(dx) + Math.abs(dy) > 10) {
      g.axis = Math.abs(dx) > Math.abs(dy) ? "x" : "y";
      // Measure from here, so nothing jumps by the distance it took to tell.
      g.cx = e.clientX;
      g.cy = e.clientY;
      dx = dy = 0;
    }
    if (g.axis === "x") {
      const dt = e.timeStamp - g.pt;
      if (dt > 0) g.vx = 0.7 * ((e.clientX - g.px) / dt) + 0.3 * g.vx;
      g.px = e.clientX;
      g.pt = e.timeStamp;
      // One image either way is as far as there is anything to show.
      setDrag(pull(between(g.base + dx, -stride(), stride())));
    } else if (g.axis === "y" && dy > 0) setView({ scale: 1, x: 0, y: dy * 0.6 });
  };

  const onPointerUp = (e: React.PointerEvent) => {
    pointers.current.delete(e.pointerId);
    const g = gesture.current;
    if (!g) return;
    if (pointers.current.size > 0) {
      // One finger left: continue as a pan from here.
      const rest = Array.from(pointers.current.values())[0] as { x: number; y: number };
      gesture.current = { start: view, dist: 0, cx: rest.x, cy: rest.y, moved: true, axis: null, base: 0, vx: 0, px: rest.x, pt: e.timeStamp };
      return;
    }
    gesture.current = null;
    if (g.axis === "x") {
      // Let go of the strip and it glides to rest, `step` images on from i.
      // A touch the system took away says nothing of where it ended: the
      // strip settles from where it was last seen.
      const taken = e.type === "pointercancel";
      const dx = (taken ? g.px : e.clientX) - g.cx;
      const total = between(g.base + dx, -stride(), stride());
      const shown = pull(total);
      // A finger that stopped before lifting did not flick.
      const v = taken || e.timeStamp - g.pt > 100 ? 0 : g.vx;
      const at = -total / stride(); // where the strip is, in images from i
      let step = 0; // untouched but for the catch, it carries on to i
      if (g.moved) {
        // Let go at a standstill, it settles on the image nearer the middle.
        if (Math.abs(v) <= 0.3) step = Math.round(at);
        // A flick goes one on from i that way, wherever the strip had got
        // to: a second flick before the first has landed is not lost.
        else if (Math.sign(dx) === Math.sign(v)) step = v < 0 ? 1 : -1;
        // Dragged one way and flicked back the other: the move is off.
        else step = v < 0 ? Math.ceil(at) : Math.floor(at);
      }
      if (!items[i + step]) step = 0;
      const rest = -step * stride();
      const speed = Math.max(0, v * Math.sign(rest - shown)) * (shown === total ? 1 : edgeGive);
      setGlide(glideTo(Math.abs(rest - shown), speed));
      setDrag(0);
      if (step) go(step);
      return;
    }
    if (g.start.scale === 1 && view.y > 90) {
      onClose();
      return;
    }
    if (g.start.scale === 1 && view.scale === 1) setView(atRest);
    if (!g.moved) {
      // Single tap on the backdrop (outside the image) dismisses the viewer.
      const t = e.target as HTMLElement | null;
      if (t && t.tagName !== "IMG" && !t.closest?.("img")) {
        onClose();
        return;
      }
      const now = Date.now();
      if (now - lastTap.current < 320) {
        // Double tap: zoom in around the tap, or back out. A long screenshot
        // goes to the width of the screen, where it can be read.
        if (view.scale > 1) setView({ scale: 1, x: 0, y: 0 });
        else zoomAt(Math.max(2.5, zoomRange().fill), e.clientX, e.clientY);
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
        <div
          ref={trackRef}
          className="viewer-track"
          style={{ transform: `translateX(calc(${-i} * (100% + ${slideGap}px) + ${drag}px))`, transition: gesture.current ? "none" : glide || undefined }}
          // At rest again: the images that slid away can go.
          onTransitionEnd={(e) => e.target === e.currentTarget && tidy()}
        >
          {items.map((it, n) =>
            n < span[0] || n > span[1] ? null : <Slide key={it.id} item={it} at={n} current={n === i} load={n === i || here} view={n === i ? view : atRest} moving={n === i && gesture.current !== null} onSettled={onSettled} />,
          )}
        </div>
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
        <div
          key={u.key}
          className={`upload-item ${u.error ? "error" : ""} ${u.previewURL ? "image" : ""}`}
          title={u.error ?? u.file.name}
          // The reason is a toast that has gone by the time it is wanted again.
          onClick={u.error ? () => toast(u.error ?? "", "error") : undefined}
        >
          {u.previewURL ? <img src={u.previewURL} alt="" /> : <IconFile />}
          {!u.attachment && !u.error && <div className="upload-progress" style={{ width: `${Math.round(u.progress * 100)}%` }} />}
          <button
            type="button"
            className="upload-remove"
            aria-label={`Remove ${u.file.name}`}
            onClick={(e) => {
              e.stopPropagation();
              onRemove(u.key);
            }}
          >
            <IconClose />
          </button>
          <span className="upload-name">{u.error ? "Failed" : u.file.name}</span>
        </div>
      ))}
    </div>
  );
}
