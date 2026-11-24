package api

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"runtime/debug"
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

// displayMinBytes is the size past which an image gets a display rendition:
// below it the original is already quick to load.
const displayMinBytes = 512 << 10

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
	if a.HasDisplay() {
		a.DisplayURL = a.URL + "/display"
	}
}

// wantsDisplay reports whether an image is given a display rendition, the
// compressed copy the app shows full screen. A small file is already quick
// to load, and of an animation only the first frame would survive.
func wantsDisplay(contentType string, size int) bool {
	return contentType != "image/gif" && size > displayMinBytes
}

// displayRendition renders an image's display rendition, or nil when the
// image is better shown as it is: one that compresses to little less than it
// already is, or that cannot be a JPEG. It may alter img.
func displayRendition(size int, img image.Image, orientation int) ([]byte, imaging.Info) {
	out, info, err := imaging.Display(img, orientation, size/4*3)
	if err != nil {
		return nil, imaging.Info{}
	}
	return out, info
}

// errImageTooLarge refuses an image that needs more memory to decode than
// the server gives image work. The limit is by memory, not by shape or by
// megapixels alone, so the message says what it comes to for this image.
func errImageTooLarge(cost imaging.Cost, memory int64) *apiError {
	pixels := float64(cost.Width) * float64(cost.Height)
	limit := pixels * float64(memory) / float64(cost.Base)
	return &apiError{Status: http.StatusRequestEntityTooLarge, Code: "image_too_large", Message: fmt.Sprintf(
		"This image is too large to process: %d×%d is %s, and an image of this kind can be up to about %s. Scale it down, or zip it to send it as a file.",
		cost.Width, cost.Height, megapixels(pixels), megapixels(limit))}
}

// megapixels writes a pixel count as "12 MP", rounded down.
func megapixels(pixels float64) string {
	if pixels < 10e6 {
		return strconv.FormatFloat(math.Floor(pixels/1e5)/10, 'f', -1, 64) + " MP"
	}
	return strconv.FormatFloat(math.Floor(pixels/1e6), 'f', 0, 64) + " MP"
}

func displayKey(id uuid.UUID) string { return fmt.Sprintf("a/%s/display.jpg", id) }

// storeUpload validates one uploaded file, renders a thumbnail for images,
// writes the bytes to object storage, and records a pending attachment.
func (s *Server) storeUpload(r *http.Request, threadID uuid.UUID, contentType, filename string, body io.Reader) (*store.Attachment, error) {
	return s.storeAttachment(r.Context(), principalFrom(r.Context()).user.ID, threadID, contentType, filename, body)
}

// storeAttachment is storeUpload for callers without a request (the
// Messenger connector storing a photo the user sent).
func (s *Server) storeAttachment(ctx context.Context, userID, threadID uuid.UUID, contentType, filename string, body io.Reader) (*store.Attachment, error) {
	if s.blobs == nil {
		return nil, errAttachmentsDisabled
	}
	data, err := io.ReadAll(io.LimitReader(body, store.MaxAttachmentBytes+1))
	if err != nil {
		return nil, errBadRequest("Could not read the upload: %v", err)
	}
	if len(data) == 0 {
		return nil, errValidation("The uploaded file is empty.")
	}
	if len(data) > store.MaxAttachmentBytes {
		return nil, &apiError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "Attachments must be at most 25 MiB."}
	}
	// A full account is refused before any decoding or upload work; the
	// insert repeats the check under a per-account lock so concurrent
	// uploads cannot slip past it together.
	if s.cfg.AttachmentQuotaBytes > 0 {
		used, err := s.store.AttachmentBytesUsed(ctx, userID)
		if err != nil {
			return nil, err
		}
		if used+int64(len(data)) > s.cfg.AttachmentQuotaBytes {
			return nil, s.errStorageQuota()
		}
	}
	contentType = normalizeContentType(contentType, filename, data)
	filename = cleanFilename(filename, contentType)
	id := store.NewID()
	in := store.AttachmentInput{ID: id, Kind: store.AttachmentFile, ContentType: contentType, Filename: filename, Size: len(data)}
	in.ObjectKey = fmt.Sprintf("a/%s/%s", id, filename)
	// Whatever is stored before a later step fails is removed again.
	var stored []blob.Object
	discard := func() {
		for _, o := range stored {
			_ = s.blobs.Delete(context.Background(), o.Key, o.ID)
		}
	}
	if imaging.ImageTypes[contentType] {
		cost := imaging.Estimate(contentType, data, thumbnailSide)
		if cost.Base > s.imageMemory {
			return nil, errImageTooLarge(cost, s.imageMemory)
		}
		// The display rendition is made when there is room for that too; the
		// image is whole without it.
		withDisplay := wantsDisplay(contentType, len(data)) && s.roomForDisplay(cost)
		wait, cancel := context.WithTimeout(ctx, 20*time.Second)
		done, err := s.admitImage(wait, cost, len(data), withDisplay)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil, errBadRequest("The upload was cancelled.")
			}
			return nil, &apiError{Status: http.StatusServiceUnavailable, Code: "busy", Message: "Too many images are being processed right now. Try again shortly.", RetryAfter: 5}
		}
		img, info, err := imaging.Decode(contentType, data)
		var thumb, display []byte
		var tinfo, dinfo imaging.Info
		if err == nil {
			// A phone photo is stored sideways with a note to turn it. Browsers
			// follow the note when they show the original, so the renditions
			// and the reported size follow it too.
			orientation := imaging.Orientation(contentType, data)
			info = info.Upright(orientation)
			if thumb, tinfo, err = imaging.Thumbnail(img, thumbnailSide, orientation); err == nil && withDisplay {
				display, dinfo = displayRendition(len(data), img, orientation)
			}
		}
		img = nil
		done()
		if err != nil {
			return nil, errValidation("The image could not be decoded: %v", err)
		}
		in.Kind = store.AttachmentImage
		in.Width, in.Height = info.Width, info.Height
		in.ThumbKey = fmt.Sprintf("a/%s/thumb.jpg", id)
		in.ThumbWidth, in.ThumbHeight = tinfo.Width, tinfo.Height
		obj, err := s.blobs.Put(ctx, in.ThumbKey, "image/jpeg", thumb)
		if err != nil {
			s.log.Error("store thumbnail", "err", err)
			return nil, errStorage
		}
		in.ThumbID = obj.ID
		stored = append(stored, blob.Object{Key: in.ThumbKey, ID: obj.ID})
		if display != nil {
			// The image is whole without it, so a failure here costs only the
			// faster view.
			if obj, err := s.blobs.Put(ctx, displayKey(id), "image/jpeg", display); err != nil {
				s.log.Warn("store display rendition", "err", err, "size", len(display))
			} else {
				in.Display = &store.DisplayInput{Key: displayKey(id), ID: obj.ID, Width: dinfo.Width, Height: dinfo.Height, Size: len(display)}
				stored = append(stored, blob.Object{Key: displayKey(id), ID: obj.ID})
			}
		}
	}
	obj, err := s.blobs.Put(ctx, in.ObjectKey, contentType, data)
	if err != nil {
		s.log.Error("store attachment", "err", err, "size", len(data))
		discard()
		return nil, errStorage
	}
	in.ObjectID = obj.ID
	stored = append(stored, blob.Object{Key: in.ObjectKey, ID: obj.ID})
	a, err := s.store.CreateAttachment(ctx, userID, threadID, in, s.cfg.AttachmentQuotaBytes)
	if err != nil {
		discard()
		if errors.Is(err, store.ErrAttachmentQuota) {
			return nil, s.errStorageQuota()
		}
		return nil, err
	}
	decorateAttachment(a)
	return a, nil
}

var errStorage = &apiError{Status: http.StatusBadGateway, Code: "storage_unavailable", Message: "The file could not be stored right now. Try again.", RetryAfter: 10}

// errStorageQuota reports that the account's attachment storage is full.
func (s *Server) errStorageQuota() *apiError {
	return &apiError{Status: http.StatusRequestEntityTooLarge, Code: "storage_quota", Message: fmt.Sprintf("This account's attachment storage is full (%s). Delete threads or messages that carry attachments to free space.", humanBytes(s.cfg.AttachmentQuotaBytes))}
}

// humanBytes formats a byte count for people.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<40:
		return strconv.FormatFloat(float64(n)/(1<<40), 'f', -1, 64) + " TiB"
	case n >= 1<<30:
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', -1, 64) + " GiB"
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', -1, 64) + " MiB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', -1, 64) + " KiB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

// imageScratchBytes is how much disk the canvases of images being worked on
// may take between them. The temp dir is shared with uploads on their way
// in, and a container's is often capped.
const imageScratchBytes = 256 << 20

// roomForDisplay reports whether the server can make an image's display
// rendition as well as decode it.
func (s *Server) roomForDisplay(cost imaging.Cost) bool {
	return cost.Base+cost.Display <= s.imageMemory && cost.Scratch <= imageScratchBytes
}

// admitImage waits for the memory that working on an image takes (decoding
// it, and rendering its thumbnail and, withDisplay, its display copy), out
// of what is set aside for image work, and returns the function that gives
// it back. However many uploads arrive together, what they hold between
// them stays within that: screenshots, a few megabytes each, are worked on
// side by side, and a photo that needs most of it has the others wait their
// turn. The file itself, size bytes in memory for as long, is counted in.
func (s *Server) admitImage(ctx context.Context, cost imaging.Cost, size int, withDisplay bool) (done func(), err error) {
	memory, scratch := cost.Base+int64(size), int64(0)
	if withDisplay {
		memory, scratch = memory+cost.Display, cost.Scratch
	}
	memory = min(memory, s.imageMemory)
	if err := s.imageWork.Acquire(ctx, memory); err != nil {
		return nil, err
	}
	if err := s.imageScratch.Acquire(ctx, scratch); err != nil {
		s.imageWork.Release(memory)
		return nil, err
	}
	return func() {
		s.imageScratch.Release(scratch)
		// What a large image leaves behind is collected before the next one
		// is let in, or the two would be in memory together after all.
		if memory >= s.imageMemory/8 {
			debug.FreeOSMemory()
		}
		s.imageWork.Release(memory)
	}, nil
}

// uploadDeadline bounds how long a request body may trickle in; SSE and
// long-polls keep the server-wide read timeout off, so uploads set their own.
func uploadDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(3 * time.Minute))
}

// activeContent are types a browser would execute or render with scripts if
// served from this origin; they are stored as opaque files instead.
var activeContent = map[string]bool{
	"text/html": true, "application/xhtml+xml": true, "image/svg+xml": true,
	"text/xml": true, "application/xml": true, "text/javascript": true, "application/javascript": true,
}

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
	if ct == "" || activeContent[ct] {
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
			s.abandonUploads(out)
			return nil, errBadRequest("Could not read %q.", h.Filename)
		}
		a, err := s.storeUpload(r, threadID, h.Header.Get("Content-Type"), h.Filename, f)
		f.Close()
		if err != nil {
			// Earlier files in this same request already stored their rows
			// and bytes; remove them so a failed request leaves nothing
			// behind (the thread rollback below only drops the rows via
			// cascade, not the bytes).
			s.abandonUploads(out)
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// abandonUploads deletes freshly stored attachments (rows plus bytes) after
// the request that stored them failed partway through.
func (s *Server) abandonUploads(list []*store.Attachment) {
	if len(list) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, a := range list {
		_ = s.store.DeleteAttachment(ctx, a.ID)
	}
	s.deleteAttachmentObjects(ctx, list)
}

func isMultipart(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "multipart/form-data"
}

// multipartMemory is how much file data of one multipart request is parsed
// into memory; anything past it spills to a file in the temp dir.
const multipartMemory = 4 << 20

// maxMultipartBody bounds a multipart request: a full set of files plus the
// form fields.
const maxMultipartBody = int64(store.MaxAttachmentsPerMessage)*store.MaxAttachmentBytes + maxJSONBody

// parseMultipart bounds the request and parses it.
func parseMultipart(r *http.Request) (*multipart.Form, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxMultipartBody)
	memory := int64(multipartMemory)
	if (r.ContentLength < 0 || r.ContentLength > multipartMemory) && !scratchWritable() {
		// No scratch space (a read-only root filesystem with nothing mounted
		// at the temp dir): hold the request in memory rather than refuse
		// every upload past a few megabytes.
		memory = maxMultipartBody
	}
	if err := r.ParseMultipartForm(memory); err != nil {
		return nil, multipartError(err)
	}
	return r.MultipartForm, nil
}

// multipartError maps a failed parse to a response. Only the request's own
// faults are the client's: a temp file that cannot be written is ours, and
// goes to the log as an internal error instead of back as a 400.
func multipartError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || errors.Is(err, multipart.ErrMessageTooLarge) {
		return &apiError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "The request is too large."}
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("multipart scratch file: %w", err)
	}
	return errBadRequest("Invalid multipart request: %v", err)
}

// scratchWritable reports whether the temp dir accepts new files, which is
// where multipart parsing spills large uploads.
func scratchWritable() bool {
	f, err := os.CreateTemp("", "finalechat-scratch-")
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return true
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
	if err := s.limitWrite(principalFrom(r.Context()), s.uploadLimiter); err != nil {
		writeError(w, err)
		return
	}
	// The multipart envelope is parsed before the thread is auto-created so
	// a malformed request leaves no empty thread behind. Storing the bytes
	// still needs the thread id, so those failures roll the fresh thread
	// back instead (see failAutoCreated).
	uploadDeadline(w)
	var form *multipart.Form
	multipart := isMultipart(r)
	if multipart {
		var err error
		form, err = parseMultipart(r)
		if err != nil {
			writeError(w, err)
			return
		}
		defer form.RemoveAll()
		n := 0
		for _, list := range form.File {
			n += len(list)
		}
		if n == 0 {
			writeError(w, errValidation("Include at least one file part."))
			return
		}
		if n > store.MaxAttachmentsPerMessage {
			writeError(w, errValidation("At most %d files per request.", store.MaxAttachmentsPerMessage))
			return
		}
	}
	thread, threadCreated, err := s.resolveThread(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	var out []*store.Attachment
	if multipart {
		var err error
		out, err = s.readUploads(r, thread.ID, form)
		if err != nil {
			s.failAutoCreated(w, r, err, thread, threadCreated)
			return
		}
		if len(out) == 0 {
			s.failAutoCreated(w, r, errValidation("Include at least one file part."), thread, threadCreated)
			return
		}
	} else {
		filename := r.Header.Get("X-Filename")
		if filename == "" {
			filename = r.URL.Query().Get("filename")
		}
		a, err := s.storeUpload(r, thread.ID, r.Header.Get("Content-Type"), filename, r.Body)
		if err != nil {
			s.failAutoCreated(w, r, err, thread, threadCreated)
			return
		}
		out = []*store.Attachment{a}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"attachments": out, "thread_id": thread.ID})
}

// GET /api/v1/attachments/{id}, and its renditions at …/thumb and
// …/display. An attachment without the rendition asked for is served as it
// is, so a client can always follow either path.
func (s *Server) serveAttachment(rendition string) http.HandlerFunc {
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
		switch {
		case rendition == "thumb" && a.HasThumb():
			key, contentType = *a.ThumbKey, "image/jpeg"
			name = strings.TrimSuffix(name, path.Ext(name)) + "-thumb.jpg"
		case rendition == "display" && a.HasDisplay():
			key, contentType = *a.DisplayKey, "image/jpeg"
			name = strings.TrimSuffix(name, path.Ext(name)) + "-display.jpg"
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
		// Revalidate on every use: the service worker keeps its own copy for
		// offline reading, and a signed-out browser must not keep serving
		// attachments from its HTTP cache for a year.
		h.Set("Cache-Control", "private, no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		// Nothing served here may run as this origin: agents upload arbitrary
		// files, and the session cookie is what a script would steal.
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		disposition := "attachment"
		if a.Kind == store.AttachmentImage || contentType == "application/pdf" || contentType == "text/plain" {
			disposition = "inline"
		}
		h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
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
		if a.HasDisplay() {
			if err := s.blobs.Delete(ctx, *a.DisplayKey, a.DisplayID); err != nil {
				s.log.Warn("delete display object", "key", *a.DisplayKey, "err", err)
			}
		}
	}
}

// runDisplayBackfill gives the images stored before display renditions
// existed one each: newest first, one at a time, with a pause between so it
// never competes with live uploads for memory. Once the queue is empty it
// only looks again now and then (an older server still running during a
// deploy stores images without one).
func (s *Server) runDisplayBackfill(ctx context.Context) {
	if s.blobs == nil {
		return
	}
	made, saved := 0, int64(0)
	wait := 30 * time.Second // let a fresh start settle first
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		found, bytes := s.backfillDisplay(ctx)
		if found {
			wait = 2 * time.Second
			if bytes > 0 {
				made, saved = made+1, saved+bytes
			}
			continue
		}
		if made > 0 {
			s.log.Info("display renditions backfilled", "images", made, "bytes_saved_per_view", saved)
			made, saved = 0, 0
		}
		wait = 10 * time.Minute
	}
}

// backfillDisplay handles one image from the queue. It reports whether there
// was one, and how many bytes smaller its display rendition is than the
// original (0 when it got none).
func (s *Server) backfillDisplay(ctx context.Context) (found bool, saved int64) {
	// The claim marks the image decided before any work, so one that cannot
	// be processed (or that takes the server down) is not tried again.
	a, err := s.store.ClaimDisplayBackfill(ctx)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			s.log.Error("claim display backfill", "err", err)
		}
		return false, 0
	}
	if a.ContentType == "image/gif" || a.Size <= displayMinBytes {
		return true, 0
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	body, _, _, err := s.blobs.Get(ctx, a.ObjectKey)
	if err != nil {
		s.log.Warn("display backfill: fetch original", "id", a.ID, "err", err)
		return true, 0
	}
	data, err := io.ReadAll(io.LimitReader(body, store.MaxAttachmentBytes+1))
	body.Close()
	if err != nil || len(data) > store.MaxAttachmentBytes {
		s.log.Warn("display backfill: read original", "id", a.ID, "err", err)
		return true, 0
	}
	// An image the server has no room to work on (one accepted when it had
	// more, say) is left as it is.
	cost := imaging.Estimate(a.ContentType, data, thumbnailSide)
	if !s.roomForDisplay(cost) {
		return true, 0
	}
	done, err := s.admitImage(ctx, cost, len(data), true)
	if err != nil {
		return true, 0
	}
	var display []byte
	var info imaging.Info
	if img, _, err := imaging.Decode(a.ContentType, data); err == nil {
		display, info = displayRendition(len(data), img, imaging.Orientation(a.ContentType, data))
	}
	done()
	if display == nil {
		return true, 0
	}
	obj, err := s.blobs.Put(ctx, displayKey(a.ID), "image/jpeg", display)
	if err != nil {
		s.log.Warn("display backfill: store rendition", "id", a.ID, "err", err)
		return true, 0
	}
	if err := s.store.SetAttachmentDisplay(ctx, a.ID, store.DisplayInput{Key: displayKey(a.ID), ID: obj.ID, Width: info.Width, Height: info.Height, Size: len(display)}); err != nil {
		// Deleted while it was being made, or the write failed: either way
		// nothing points at the object.
		_ = s.blobs.Delete(context.Background(), displayKey(a.ID), obj.ID)
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("display backfill: record rendition", "id", a.ID, "err", err)
		}
		return true, 0
	}
	return true, int64(len(data) - len(display))
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
