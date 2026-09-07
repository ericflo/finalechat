package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/blob"
	"github.com/ericflo/finalechat/internal/imaging"
	"github.com/ericflo/finalechat/internal/store"
)

// thumbnailSide is the longest side of generated image thumbnails.
const thumbnailSide = 640

var errAttachmentsDisabled = &apiError{Status: http.StatusServiceUnavailable, Code: "attachments_disabled", Message: "Attachments are not configured on this server."}

// decorate fills the URLs of a message's attachments.
func decorate(msgs ...*store.Message) {
	for _, m := range msgs {
		for _, a := range m.Attachments {
			decorateAttachment(a)
		}
	}
}

func decorateAttachment(a *store.Attachment) {
	a.URL = "/api/v1/attachments/" + a.ID.String()
	if a.HasThumb() {
		a.ThumbURL = a.URL + "/thumb"
	}
}

// storeUpload validates one uploaded file, renders a thumbnail for images,
// writes the bytes to object storage, and records a pending attachment.
func (s *Server) storeUpload(r *http.Request, threadID uuid.UUID, contentType, filename string, body io.Reader) (*store.Attachment, error) {
	if s.blobs == nil {
		return nil, errAttachmentsDisabled
	}
	p := principalFrom(r.Context())
	data, err := io.ReadAll(io.LimitReader(body, store.MaxAttachmentBytes+1))
	if err != nil {
		return nil, errBadRequest("Could not read the upload: %v", err)
	}
	if len(data) == 0 {
		return nil, errValidation("The uploaded file is empty.")
	}
	if len(data) > store.MaxAttachmentBytes {
		return nil, &apiError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "Attachments must be at most 10 MiB."}
	}
	contentType = normalizeContentType(contentType, filename, data)
	filename = cleanFilename(filename, contentType)
	id := store.NewID()
	in := store.AttachmentInput{ID: id, Kind: store.AttachmentFile, ContentType: contentType, Filename: filename, Size: len(data)}
	in.ObjectKey = fmt.Sprintf("a/%s/%s", id, filename)
	var thumb []byte
	if imaging.ImageTypes[contentType] {
		img, info, err := imaging.Decode(contentType, data)
		if err != nil {
			return nil, errValidation("The image could not be decoded: %v", err)
		}
		thumb, tinfo, err := imaging.Thumbnail(img, thumbnailSide)
		if err != nil {
			return nil, err
		}
		in.Kind = store.AttachmentImage
		in.Width, in.Height = info.Width, info.Height
		in.ThumbKey = fmt.Sprintf("a/%s/thumb.jpg", id)
		in.ThumbWidth, in.ThumbHeight = tinfo.Width, tinfo.Height
		obj, err := s.blobs.Put(r.Context(), in.ThumbKey, "image/jpeg", thumb)
		if err != nil {
			s.log.Error("store thumbnail", "err", err)
			return nil, errStorage
		}
		in.ThumbID = obj.ID
	}
	obj, err := s.blobs.Put(r.Context(), in.ObjectKey, contentType, data)
	if err != nil {
		s.log.Error("store attachment", "err", err, "size", len(data))
		if thumb != nil {
			_ = s.blobs.Delete(context.Background(), in.ThumbKey, in.ThumbID)
		}
		return nil, errStorage
	}
	in.ObjectID = obj.ID
	a, err := s.store.CreateAttachment(r.Context(), p.user.ID, threadID, in)
	if err != nil {
		_ = s.blobs.Delete(context.Background(), in.ObjectKey, in.ObjectID)
		if in.ThumbKey != "" {
			_ = s.blobs.Delete(context.Background(), in.ThumbKey, in.ThumbID)
		}
		return nil, err
	}
	decorateAttachment(a)
	return a, nil
}

var errStorage = &apiError{Status: http.StatusBadGateway, Code: "storage_unavailable", Message: "The file could not be stored right now. Try again."}

// normalizeContentType trusts the declared type when it is specific, sniffs
// otherwise, and maps a few common extensions.
func normalizeContentType(declared, filename string, data []byte) string {
	ct := strings.ToLower(strings.TrimSpace(declared))
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		ct = mt
	}
	if ct == "" || ct == "application/octet-stream" || ct == "binary/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(path.Ext(filename))); byExt != "" {
			ct, _, _ = mime.ParseMediaType(byExt)
		}
	}
	sniffed := http.DetectContentType(data)
	if imaging.ImageTypes[sniffed] {
		if !imaging.ImageTypes[ct] {
			ct = sniffed
		}
	} else if imaging.ImageTypes[ct] {
		// Declared an image but the bytes disagree; keep it as a plain file.
		ct = "application/octet-stream"
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct
}

func cleanFilename(name, contentType string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "." || name == "/" {
		name = ""
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '/' {
			return -1
		}
		return r
	}, name)
	if name == "" {
		ext := ".bin"
		switch contentType {
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		case "text/plain":
			ext = ".txt"
		case "application/pdf":
			ext = ".pdf"
		}
		name = "attachment" + ext
	}
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	return name
}

// readUploads collects files from a multipart request. The field name is
// ignored so `file`, `files` and `attachment` all work.
func (s *Server) readUploads(r *http.Request, threadID uuid.UUID, form *multipart.Form) ([]*store.Attachment, error) {
	var headers []*multipart.FileHeader
	for _, list := range form.File {
		headers = append(headers, list...)
	}
	if len(headers) > store.MaxAttachmentsPerMessage {
		return nil, errValidation("At most %d files per request.", store.MaxAttachmentsPerMessage)
	}
	out := make([]*store.Attachment, 0, len(headers))
	for _, h := range headers {
		f, err := h.Open()
		if err != nil {
			return nil, errBadRequest("Could not read %q.", h.Filename)
		}
		a, err := s.storeUpload(r, threadID, h.Header.Get("Content-Type"), h.Filename, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func isMultipart(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "multipart/form-data"
}

// parseMultipart bounds the request and parses it.
func parseMultipart(r *http.Request) (*multipart.Form, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, int64(store.MaxAttachmentsPerMessage)*store.MaxAttachmentBytes+maxJSONBody)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, &apiError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "The request is too large."}
		}
		return nil, errBadRequest("Invalid multipart request: %v", err)
	}
	return r.MultipartForm, nil
}

// POST /api/v1/threads/{thread}/attachments
//
// Accepts multipart/form-data with one or more file parts, or a raw body
// whose Content-Type names the file type (filename from X-Filename or the
// filename query parameter). Returns the pending attachments; attach them to
// a message through its `attachments` field within 24 hours.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		writeError(w, errAttachmentsDisabled)
		return
	}
	thread, _, err := s.resolveThread(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	var out []*store.Attachment
	if isMultipart(r) {
		form, err := parseMultipart(r)
		if err != nil {
			writeError(w, err)
			return
		}
		defer form.RemoveAll()
		out, err = s.readUploads(r, thread.ID, form)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(out) == 0 {
			writeError(w, errValidation("Include at least one file part."))
			return
		}
	} else {
		filename := r.Header.Get("X-Filename")
		if filename == "" {
			filename = r.URL.Query().Get("filename")
		}
		a, err := s.storeUpload(r, thread.ID, r.Header.Get("Content-Type"), filename, r.Body)
		if err != nil {
			writeError(w, err)
			return
		}
		out = []*store.Attachment{a}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"attachments": out, "thread_id": thread.ID})
}

// GET /api/v1/attachments/{id} and /api/v1/attachments/{id}/thumb
func (s *Server) serveAttachment(thumb bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.blobs == nil {
			writeError(w, errAttachmentsDisabled)
			return
		}
		p := principalFrom(r.Context())
		id, err := parseUUID(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		a, err := s.store.GetAttachment(r.Context(), p.user.ID, id)
		if err != nil {
			writeError(w, err)
			return
		}
		key, contentType, name := a.ObjectKey, a.ContentType, a.Filename
		if thumb && a.HasThumb() {
			key, contentType = *a.ThumbKey, "image/jpeg"
			name = strings.TrimSuffix(name, path.Ext(name)) + "-thumb.jpg"
		}
		body, _, size, err := s.blobs.Get(r.Context(), key)
		if errors.Is(err, blob.ErrNotFound) {
			writeError(w, errNotFound)
			return
		}
		if err != nil {
			s.log.Error("fetch attachment", "err", err, "id", id)
			writeError(w, errStorage)
			return
		}
		defer body.Close()
		h := w.Header()
		h.Set("Content-Type", contentType)
		if size > 0 {
			h.Set("Content-Length", strconv.FormatInt(size, 10))
		}
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
		h.Set("X-Content-Type-Options", "nosniff")
		disposition := "attachment"
		if a.Kind == store.AttachmentImage || contentType == "application/pdf" || strings.HasPrefix(contentType, "text/") {
			disposition = "inline"
		}
		h.Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, name))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.Copy(w, body)
	}
}

// deleteAttachmentObjects removes the stored bytes for attachments; failures
// are logged rather than surfaced because the metadata is already gone or
// about to be.
func (s *Server) deleteAttachmentObjects(ctx context.Context, list []*store.Attachment) {
	if s.blobs == nil {
		return
	}
	for _, a := range list {
		if err := s.blobs.Delete(ctx, a.ObjectKey, a.ObjectID); err != nil {
			s.log.Warn("delete attachment object", "key", a.ObjectKey, "err", err)
		}
		if a.HasThumb() {
			if err := s.blobs.Delete(ctx, *a.ThumbKey, a.ThumbID); err != nil {
				s.log.Warn("delete thumbnail object", "key", *a.ThumbKey, "err", err)
			}
		}
	}
}

// pruneOrphanAttachments deletes uploads that were never attached.
func (s *Server) pruneOrphanAttachments(ctx context.Context) {
	orphans, err := s.store.OrphanAttachments(ctx, 24*time.Hour, 100)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("list orphan attachments", "err", err)
		}
		return
	}
	for _, a := range orphans {
		s.deleteAttachmentObjects(ctx, []*store.Attachment{a})
		if err := s.store.DeleteAttachment(ctx, a.ID); err != nil && ctx.Err() == nil {
			s.log.Error("delete orphan attachment", "id", a.ID, "err", err)
		}
	}
	if len(orphans) > 0 {
		s.log.Info("pruned orphan attachments", "count", len(orphans))
	}
}
