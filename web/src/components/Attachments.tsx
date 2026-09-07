import { useEffect, useState } from "react";
import type { Attachment } from "../lib/types";
import { IconClose, IconDownload, IconFile } from "./Icons";

export function humanSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

/** Renders a message's attachments: an image grid plus file chips. */
export function AttachmentList({ items, onOpen }: { items: Attachment[]; onOpen: (a: Attachment) => void }) {
  const images = items.filter((a) => a.kind === "image");
  const files = items.filter((a) => a.kind !== "image");
  return (
    <>
      {images.length > 0 && (
        <div className={`att-grid ${images.length === 1 ? "single" : ""}`}>
          {images.map((a) => {
            const w = a.thumb_width || a.width || 4;
            const h = a.thumb_height || a.height || 3;
            return (
              <button key={a.id} type="button" className="att-image" style={{ aspectRatio: `${w} / ${h}` }} onClick={() => onOpen(a)} aria-label={`Open ${a.filename}`}>
                <img src={a.thumb_url ?? a.url} alt={a.filename} loading="lazy" decoding="async" />
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

/** Full-screen image viewer. */
export function ImageViewer({ item, onClose }: { item: Attachment; onClose: () => void }) {
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [onClose]);
  return (
    <div className="viewer" role="dialog" aria-label={item.filename} onClick={onClose}>
      <div className="viewer-bar" onClick={(e) => e.stopPropagation()}>
        <span className="viewer-title">
          {item.filename}
          <span className="att-meta">
            {" "}
            · {item.width}×{item.height} · {humanSize(item.size)}
          </span>
        </span>
        <a className="icon-btn" href={item.url} download={item.filename} target="_blank" rel="noopener" aria-label="Download">
          <IconDownload />
        </a>
        <button type="button" className="icon-btn" onClick={onClose} aria-label="Close">
          <IconClose />
        </button>
      </div>
      <div className="viewer-body">
        {!loaded && <span className="spinner" />}
        <img src={item.url} alt={item.filename} onLoad={() => setLoaded(true)} style={{ opacity: loaded ? 1 : 0 }} onClick={(e) => e.stopPropagation()} />
      </div>
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
        <div key={u.key} className={`upload-item ${u.error ? "error" : ""}`} title={u.error ?? u.file.name}>
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
