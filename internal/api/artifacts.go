package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ericflo/finalechat/internal/artifact"
	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/store"
	"github.com/google/uuid"
)

func (s *Server) artifactRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/v1/threads/{thread}/artifacts/{key}", s.handlePutArtifact)
	mux.HandleFunc("GET /api/v1/threads/{thread}/artifacts", s.handleListArtifacts)
	mux.HandleFunc("GET /api/v1/artifacts/{id}", s.handleGetArtifact)
	mux.HandleFunc("DELETE /api/v1/artifacts/{id}", s.sessionOnly(s.handleDeleteArtifact))
	mux.HandleFunc("POST /api/v1/artifacts/{id}/blobs/check", s.handleCheckArtifactBlobs)
	mux.HandleFunc("PUT /api/v1/artifacts/{id}/blobs/{hash}", s.handlePutArtifactBlob)
	mux.HandleFunc("POST /api/v1/artifacts/{id}/revisions", s.handleCommitArtifact)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/revisions", s.handleListArtifactRevisions)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/revisions/{revision}", s.handleGetArtifactRevision)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/revisions/{revision}/files/{file...}", s.handleArtifactFile)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/revisions/{revision}/preview", s.sessionOnly(s.handleArtifactPreview))
	mux.HandleFunc("GET /api/v1/artifacts/{id}/revisions/{revision}/download", s.handleArtifactDownload)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/revisions/{revision}/message", s.handleArtifactMessage)
}

func (s *Server) artifactFor(r *http.Request) (*store.Artifact, error) {
	if s.blobs == nil {
		return nil, errAttachmentsDisabled
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return s.store.GetArtifact(r.Context(), principalFrom(r.Context()).user.ID, id)
}

func (s *Server) revisionFor(r *http.Request) (*store.Artifact, *store.ArtifactRevision, error) {
	a, err := s.artifactFor(r)
	if err != nil {
		return nil, nil, err
	}
	var id uuid.UUID
	if r.PathValue("revision") == "current" {
		if a.CurrentRevisionID == nil {
			return a, nil, errNotFound
		}
		id = *a.CurrentRevisionID
	} else {
		id, err = parseUUID(r.PathValue("revision"))
		if err != nil {
			return a, nil, err
		}
	}
	rev, err := s.store.GetArtifactRevision(r.Context(), a.UserID, a.ID, id)
	if err != nil {
		return a, nil, err
	}
	if value := r.URL.Query().Get("viewer"); value != "" {
		if r.URL.Query().Get("surface") == "settings" {
			return a, nil, errValidation("viewer replacement cannot open a settings surface")
		}
		viewerID, err := parseUUID(value)
		if err != nil {
			return a, nil, err
		}
		viewer, err := s.store.GetArtifactRevision(r.Context(), a.UserID, a.ID, viewerID)
		if err != nil {
			return a, nil, err
		}
		manifest, err := artifact.Reinterpret(rev.Manifest, viewer.Manifest, rev.ID.String(), viewer.ID.String())
		if err != nil {
			return a, nil, errValidation("%v", err)
		}
		combined := *rev
		combined.Manifest = manifest
		raw, _ := json.Marshal(manifest)
		combined.ManifestSHA256 = artifact.Digest(raw)
		rev = &combined
	}
	return a, rev, nil
}

func (s *Server) artifactEvent(ctx context.Context, a *store.Artifact, typ string) {
	s.bus.Publish(ctx, bus.Event{Type: typ, UserID: a.UserID.String(), ThreadID: a.ThreadID.String(), ArtifactID: a.ID.String()})
}

func (s *Server) handlePutArtifact(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		writeError(w, errAttachmentsDisabled)
		return
	}
	if err := s.limitWrite(principalFrom(r.Context()), s.artifactLimiter); err != nil {
		writeError(w, err)
		return
	}
	key := r.PathValue("key")
	if len(key) > 120 || !artifact.ValidPath(key) || strings.Contains(key, "/") {
		writeError(w, errValidation("artifact key must be a portable name of at most 120 bytes"))
		return
	}
	var in struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 300 {
		writeError(w, errValidation("title must be 1 to 300 bytes"))
		return
	}
	t, _, err := s.resolveThread(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	a, err := s.store.UpsertArtifact(r.Context(), principalFrom(r.Context()).user.ID, t.ID, key, in.Title)
	if err != nil {
		writeError(w, err)
		return
	}
	s.artifactEvent(context.WithoutCancel(r.Context()), a, bus.ArtifactUpdated)
	writeJSON(w, 200, map[string]any{"artifact": a})
}

func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	t, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	a, err := s.store.ListArtifacts(r.Context(), principalFrom(r.Context()).user.ID, t.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"artifacts": a})
}

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	a, err := s.artifactFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	out := map[string]any{"artifact": a, "limits": map[string]any{"blob_bytes": artifact.MaxBlobBytes, "file_bytes": artifact.MaxFileBytes, "revision_bytes": artifact.MaxRevisionBytes, "account_bytes": artifact.MaxAccountBytes, "files": artifact.MaxFiles, "chunks": artifact.MaxChunks}}
	if a.CurrentRevisionID != nil {
		rev, err := s.store.GetArtifactRevision(r.Context(), a.UserID, a.ID, *a.CurrentRevisionID)
		if err != nil {
			writeError(w, err)
			return
		}
		out["revision"] = rev
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	a, err := s.artifactFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.DeleteArtifact(r.Context(), a.UserID, a.ID); err != nil {
		writeError(w, err)
		return
	}
	s.artifactEvent(context.WithoutCancel(r.Context()), a, bus.ArtifactDeleted)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleCheckArtifactBlobs(w http.ResponseWriter, r *http.Request) {
	a, err := s.artifactFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Hashes []string `json:"hashes"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if len(in.Hashes) > artifact.MaxChunks {
		writeError(w, errValidation("too many hashes"))
		return
	}
	for _, h := range in.Hashes {
		if !artifact.ValidDigest(h) {
			writeError(w, errValidation("invalid sha256"))
			return
		}
	}
	missing, err := s.store.MissingArtifactBlobs(r.Context(), a.UserID, in.Hashes)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"missing": missing})
}

func (s *Server) handlePutArtifactBlob(w http.ResponseWriter, r *http.Request) {
	a, err := s.artifactFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.limitWrite(principalFrom(r.Context()), s.artifactLimiter); err != nil {
		writeError(w, err)
		return
	}
	h := r.PathValue("hash")
	if !artifact.ValidDigest(h) {
		writeError(w, errValidation("invalid sha256"))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, artifact.MaxBlobBytes+1))
	if err != nil {
		writeError(w, errBadRequest("could not read blob"))
		return
	}
	if len(raw) > artifact.MaxBlobBytes {
		writeError(w, &apiError{Status: 413, Code: "too_large", Message: "Artifact chunks are limited to 1 MiB."})
		return
	}
	if len(raw) == 0 || artifact.Digest(raw) != h {
		writeError(w, errValidation("blob is empty or does not match its sha256"))
		return
	}
	b, err := s.store.ReserveArtifactBlob(r.Context(), a.UserID, a.ID, h, int64(len(raw)))
	if errors.Is(err, store.ErrUploadBusy) {
		writeError(w, &apiError{Status: 409, Code: "upload_in_progress", Message: "This chunk is being uploaded. Retry shortly.", RetryAfter: 2})
		return
	}
	if errors.Is(err, store.ErrQuota) {
		writeError(w, &apiError{Status: 413, Code: "storage_quota", Message: err.Error()})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if b.State == "ready" {
		writeJSON(w, 200, map[string]any{"sha256": h, "created": false})
		return
	}
	obj, err := s.blobs.Put(r.Context(), b.ObjectKey, "application/octet-stream", raw)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancel()
	if err != nil {
		_ = s.store.AbandonArtifactBlob(cleanupCtx, a.UserID, b)
		writeError(w, errStorage)
		return
	}
	if err = s.store.CompleteArtifactBlob(cleanupCtx, a.UserID, b, obj.ID); err != nil {
		_ = s.store.QueueArtifactObjectDeletion(cleanupCtx, obj.Key, obj.ID)
		_ = s.store.AbandonArtifactBlob(cleanupCtx, a.UserID, b)
		writeError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"sha256": h, "created": true})
}

func (s *Server) handleCommitArtifact(w http.ResponseWriter, r *http.Request) {
	a, err := s.artifactFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.limitWrite(principalFrom(r.Context()), s.artifactLimiter); err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Manifest json.RawMessage `json:"manifest"`
		Previous *uuid.UUID      `json:"previous_revision_id"`
		Key      string          `json:"client_key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if k := r.Header.Get("Idempotency-Key"); k != "" {
		in.Key = k
	}
	if in.Key == "" || len(in.Key) > 200 {
		writeError(w, errValidation("client_key of at most 200 bytes is required"))
		return
	}
	var m artifact.Manifest
	dec := json.NewDecoder(bytes.NewReader(in.Manifest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		writeError(w, errValidation("invalid manifest: %v", err))
		return
	}
	if err := m.Validate(); err != nil {
		writeError(w, errValidation("%v", err))
		return
	}
	rev, created, err := s.store.CommitArtifactRevision(r.Context(), a.UserID, a.ID, in.Previous, in.Key, m)
	if err != nil {
		writeError(w, err)
		return
	}
	status := 200
	if created {
		status = 201
		s.artifactEvent(context.WithoutCancel(r.Context()), a, bus.ArtifactUpdated)
	}
	writeJSON(w, status, map[string]any{"revision": rev, "created": created})
}

func (s *Server) handleListArtifactRevisions(w http.ResponseWriter, r *http.Request) {
	a, err := s.artifactFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var before *uuid.UUID
	if v := r.URL.Query().Get("before"); v != "" {
		id, err := parseUUID(v)
		if err != nil {
			writeError(w, err)
			return
		}
		before = &id
	}
	revs, err := s.store.ListArtifactRevisions(r.Context(), a.UserID, a.ID, before)
	if err != nil {
		writeError(w, err)
		return
	}
	out := map[string]any{"revisions": revs}
	if len(revs) == 50 {
		out["next_before"] = revs[49].ID
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleGetArtifactRevision(w http.ResponseWriter, r *http.Request) {
	_, rev, err := s.revisionFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"revision": rev})
}

// copyArtifactFile bounds memory to one verified chunk. A partial read still
// verifies every touched chunk; a full read also verifies the complete file.
func (s *Server) copyArtifactFile(ctx context.Context, w io.Writer, userID uuid.UUID, f artifact.File, offset, length int64) error {
	if offset < 0 || length < 0 || offset > f.Size || length > f.Size-offset {
		return fmt.Errorf("invalid file range")
	}
	end := offset + length
	position := int64(0)
	sum := sha256.New()
	for _, c := range f.Chunks {
		start := position
		position += c.Size
		if position <= offset || start >= end {
			continue
		}
		b, err := s.store.GetArtifactBlob(ctx, userID, c.SHA256)
		if err != nil {
			return err
		}
		body, _, _, err := s.blobs.Get(ctx, b.ObjectKey)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(body, c.Size+1))
		_ = body.Close()
		if err != nil {
			return err
		}
		if int64(len(raw)) != c.Size || artifact.Digest(raw) != c.SHA256 {
			return fmt.Errorf("corrupt artifact chunk")
		}
		lo, hi := int64(0), c.Size
		if offset > start {
			lo = offset - start
		}
		if end < position {
			hi = end - start
		}
		_, _ = sum.Write(raw[lo:hi])
		if _, err := w.Write(raw[lo:hi]); err != nil {
			return err
		}
	}
	if offset == 0 && length == f.Size && hex.EncodeToString(sum.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("file digest does not match manifest")
	}
	return nil
}

func (s *Server) handleArtifactFile(w http.ResponseWriter, r *http.Request) {
	a, rev, err := s.revisionFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	name := r.PathValue("file")
	if !artifact.ValidPath(name) {
		writeError(w, errNotFound)
		return
	}
	f, ok := rev.Manifest.Find(name)
	if !ok {
		writeError(w, errNotFound)
		return
	}
	offset, length := int64(0), f.Size
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.ParseInt(v, 10, 64)
		if err != nil || offset < 0 || offset > f.Size {
			writeError(w, errValidation("invalid offset"))
			return
		}
		length = f.Size - offset
	}
	if v := r.URL.Query().Get("length"); v != "" {
		length, err = strconv.ParseInt(v, 10, 64)
		if err != nil || length < 0 || length > f.Size-offset {
			writeError(w, errValidation("invalid length"))
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("X-Artifact-SHA256", f.SHA256)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
	if err := s.copyArtifactFile(r.Context(), w, a.UserID, f, offset, length); err != nil {
		s.log.Error("artifact file read", "artifact_id", a.ID, "error", err)
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) handleArtifactPreview(w http.ResponseWriter, r *http.Request) {
	a, rev, err := s.revisionFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	entry := rev.Manifest.Entrypoint
	if r.URL.Query().Get("surface") == "settings" {
		entry = rev.Manifest.SettingsEntrypoint
	}
	f, ok := rev.Manifest.Find(entry)
	if !ok || f.Size > artifact.MaxPreviewBytes {
		writeError(w, errNotFound)
		return
	}
	var buf bytes.Buffer
	if err := s.copyArtifactFile(r.Context(), &buf, a.UserID, f, 0, f.Size); err != nil {
		writeError(w, errStorage)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Referrer-Policy", "no-referrer")
	// sandbox in the response protects direct navigations too. The parent
	// also uses sandbox=allow-scripts; the uploaded page never gets our origin.
	h.Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'unsafe-inline'; img-src data: blob:; media-src data: blob:; font-src data:; connect-src 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleArtifactDownload(w http.ResponseWriter, r *http.Request) {
	a, rev, err := s.revisionFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Key + "-" + rev.ID.String() + ".zip"}))
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	z := zip.NewWriter(w)
	f, err := z.Create("manifest.json")
	if err == nil {
		err = json.NewEncoder(f).Encode(rev.Manifest)
	}
	for _, file := range rev.Manifest.Files {
		if err != nil {
			break
		}
		var dst io.Writer
		dst, err = z.Create(file.Path)
		if err == nil {
			err = s.copyArtifactFile(r.Context(), dst, a.UserID, file, 0, file.Size)
		}
	}
	if err == nil {
		err = z.Close()
	}
	if err != nil {
		s.log.Error("artifact archive read", "artifact_id", a.ID, "error", err)
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) pruneArtifactObjects(ctx context.Context) {
	if s.blobs == nil {
		return
	}
	if err := s.store.PruneArtifactBlobs(ctx); err != nil {
		s.log.Error("prune artifact blobs", "error", err)
		return
	}
	items, err := s.store.ArtifactObjectDeletions(ctx)
	if err != nil {
		s.log.Error("list artifact deletions", "error", err)
		return
	}
	for _, d := range items {
		if ctx.Err() != nil {
			return
		}
		err := s.blobs.DeleteAll(ctx, d.Key)
		if err != nil {
			s.log.Warn("delete artifact object", "error", err)
		}
		if e := s.store.FinishArtifactObjectDeletion(ctx, d, err == nil); e != nil {
			s.log.Error("record artifact deletion", "error", e)
		}
	}
}
