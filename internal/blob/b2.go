package blob

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// B2 talks to Backblaze B2's native API with an application key restricted to
// one bucket. It re-authorizes when a token expires and retries an upload
// once with a fresh upload URL, as the B2 integration guide requires.
type B2 struct {
	keyID  string
	key    string
	bucket string
	client *http.Client

	mu           sync.Mutex
	authToken    string
	apiURL       string
	downloadURL  string
	bucketID     string
	authorizedAt time.Time
}

// NewB2 creates a client. bucket is the bucket name the key is scoped to.
func NewB2(keyID, key, bucket string) *B2 {
	return &B2{keyID: keyID, key: key, bucket: bucket, client: &http.Client{Timeout: 5 * time.Minute}}
}

func (b *B2) Name() string { return "b2:" + b.bucket }

// authResponse is the v4 shape; bucket-restricted keys are only accepted by
// the v4 authorize endpoint.
type authResponse struct {
	AuthorizationToken string `json:"authorizationToken"`
	APIInfo            struct {
		StorageAPI struct {
			APIURL      string `json:"apiUrl"`
			DownloadURL string `json:"downloadUrl"`
			Allowed     struct {
				Buckets []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"buckets"`
			} `json:"allowed"`
		} `json:"storageApi"`
	} `json:"apiInfo"`
}

// authorize refreshes credentials when missing or older than 23 hours.
func (b *B2) authorize(ctx context.Context, force bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !force && b.authToken != "" && time.Since(b.authorizedAt) < 23*time.Hour {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.backblazeb2.com/b2api/v4/b2_authorize_account", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(b.keyID, b.key)
	res, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("b2 authorize: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("b2 authorize: %s: %s", res.Status, readErr(res.Body))
	}
	var auth authResponse
	if err := json.NewDecoder(res.Body).Decode(&auth); err != nil {
		return fmt.Errorf("b2 authorize: decode: %w", err)
	}
	storage := auth.APIInfo.StorageAPI
	bucketID := ""
	for _, bk := range storage.Allowed.Buckets {
		if bk.Name == b.bucket {
			bucketID = bk.ID
		}
	}
	if bucketID == "" {
		names := make([]string, 0, len(storage.Allowed.Buckets))
		for _, bk := range storage.Allowed.Buckets {
			names = append(names, bk.Name)
		}
		return fmt.Errorf("b2 authorize: the application key must be restricted to bucket %q (it allows %v)", b.bucket, names)
	}
	b.authToken, b.apiURL, b.downloadURL, b.bucketID = auth.AuthorizationToken, storage.APIURL, storage.DownloadURL, bucketID
	b.authorizedAt = time.Now()
	return nil
}

func (b *B2) creds() (token, apiURL, downloadURL, bucketID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authToken, b.apiURL, b.downloadURL, b.bucketID
}

func readErr(r io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(r, 2048))
	return strings.TrimSpace(string(raw))
}

// Ping verifies the credentials.
func (b *B2) Ping(ctx context.Context) error { return b.authorize(ctx, true) }

type uploadURLResponse struct {
	UploadURL          string `json:"uploadUrl"`
	AuthorizationToken string `json:"authorizationToken"`
}

func (b *B2) getUploadURL(ctx context.Context) (uploadURLResponse, error) {
	token, apiURL, _, bucketID := b.creds()
	body, _ := json.Marshal(map[string]string{"bucketId": bucketID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/b2api/v4/b2_get_upload_url", bytes.NewReader(body))
	if err != nil {
		return uploadURLResponse{}, err
	}
	req.Header.Set("Authorization", token)
	res, err := b.client.Do(req)
	if err != nil {
		return uploadURLResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return uploadURLResponse{}, errExpired
	}
	if res.StatusCode != http.StatusOK {
		return uploadURLResponse{}, fmt.Errorf("b2 get_upload_url: %s: %s", res.Status, readErr(res.Body))
	}
	var out uploadURLResponse
	return out, json.NewDecoder(res.Body).Decode(&out)
}

var errExpired = errors.New("b2 authorization expired")

// Put uploads the bytes. Failed uploads are retried once with a new upload
// URL, and an expired account token is refreshed transparently.
func (b *B2) Put(ctx context.Context, key, contentType string, data []byte) (Object, error) {
	if err := b.authorize(ctx, false); err != nil {
		return Object{}, err
	}
	sum := sha1.Sum(data)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		up, err := b.getUploadURL(ctx)
		if errors.Is(err, errExpired) {
			if err := b.authorize(ctx, true); err != nil {
				return Object{}, err
			}
			continue
		}
		if err != nil {
			lastErr = err
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.UploadURL, bytes.NewReader(data))
		if err != nil {
			return Object{}, err
		}
		req.ContentLength = int64(len(data))
		req.Header.Set("Authorization", up.AuthorizationToken)
		req.Header.Set("X-Bz-File-Name", encodeName(key))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Bz-Content-Sha1", hex.EncodeToString(sum[:]))
		res, err := b.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		var out struct {
			FileID   string `json:"fileId"`
			FileName string `json:"fileName"`
			Size     int64  `json:"contentLength"`
		}
		if res.StatusCode == http.StatusOK {
			err = json.NewDecoder(res.Body).Decode(&out)
			res.Body.Close()
			if err != nil {
				return Object{}, err
			}
			return Object{Key: key, ID: out.FileID, Size: out.Size}, nil
		}
		msg := readErr(res.Body)
		res.Body.Close()
		lastErr = fmt.Errorf("b2 upload: %s: %s", res.Status, msg)
		if res.StatusCode == http.StatusUnauthorized {
			_ = b.authorize(ctx, true)
		}
	}
	return Object{}, lastErr
}

// encodeName percent-encodes an object name the way B2 expects.
func encodeName(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// Get streams the object by name.
func (b *B2) Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	if err := b.authorize(ctx, false); err != nil {
		return nil, "", 0, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, _, downloadURL, _ := b.creds()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL+"/file/"+url.PathEscape(b.bucket)+"/"+encodeName(key), nil)
		if err != nil {
			return nil, "", 0, err
		}
		req.Header.Set("Authorization", token)
		res, err := b.client.Do(req)
		if err != nil {
			return nil, "", 0, fmt.Errorf("b2 download: %w", err)
		}
		switch res.StatusCode {
		case http.StatusOK:
			return res.Body, res.Header.Get("Content-Type"), res.ContentLength, nil
		case http.StatusNotFound:
			res.Body.Close()
			return nil, "", 0, ErrNotFound
		case http.StatusUnauthorized:
			res.Body.Close()
			if err := b.authorize(ctx, true); err != nil {
				return nil, "", 0, err
			}
			continue
		default:
			msg := readErr(res.Body)
			res.Body.Close()
			return nil, "", 0, fmt.Errorf("b2 download: %s: %s", res.Status, msg)
		}
	}
	return nil, "", 0, errExpired
}

// Delete removes one file version. Unknown versions are ignored.
// DeleteAll is used only for unique retired artifact names. Listing by exact
// name recovers duplicate versions created by a lost upload response. It needs
// B2's listFiles capability in addition to readFiles/writeFiles/deleteFiles.
func (b *B2) DeleteAll(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("empty object name")
	}
	if err := b.authorize(ctx, false); err != nil {
		return err
	}
	for page := 0; page < 100; page++ {
		token, apiURL, _, bucketID := b.creds()
		query := url.Values{"bucketId": {bucketID}, "prefix": {key}, "startFileName": {key}, "maxFileCount": {"1000"}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/b2api/v4/b2_list_file_versions?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", token)
		res, err := b.client.Do(req)
		if err != nil {
			return err
		}
		if res.StatusCode == http.StatusUnauthorized {
			res.Body.Close()
			if err := b.authorize(ctx, true); err != nil {
				return err
			}
			continue
		}
		if res.StatusCode != http.StatusOK {
			msg := readErr(res.Body)
			res.Body.Close()
			return fmt.Errorf("b2 list versions: %s: %s", res.Status, msg)
		}
		var out struct {
			Files []struct {
				Name   string `json:"fileName"`
				ID     string `json:"fileId"`
				Action string `json:"action"`
			} `json:"files"`
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out)
		res.Body.Close()
		if err != nil {
			return err
		}
		removed := 0
		for _, f := range out.Files {
			if f.Name != key {
				continue
			}
			if f.ID == "" || (f.Action != "upload" && f.Action != "hide") {
				return fmt.Errorf("unexpected artifact object version")
			}
			if err := b.Delete(ctx, key, f.ID); err != nil {
				return err
			}
			removed++
		}
		if removed == 0 {
			return nil
		}
	}
	return fmt.Errorf("artifact object cleanup exceeded page limit")
}

func (b *B2) Delete(ctx context.Context, key, id string) error {
	if id == "" {
		return nil
	}
	if err := b.authorize(ctx, false); err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, apiURL, _, _ := b.creds()
		body, _ := json.Marshal(map[string]string{"fileName": key, "fileId": id})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/b2api/v4/b2_delete_file_version", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", token)
		res, err := b.client.Do(req)
		if err != nil {
			return fmt.Errorf("b2 delete: %w", err)
		}
		msg := readErr(res.Body)
		res.Body.Close()
		switch {
		case res.StatusCode == http.StatusOK:
			return nil
		case res.StatusCode == http.StatusBadRequest && strings.Contains(msg, "not_present"):
			return nil
		case res.StatusCode == http.StatusUnauthorized:
			if err := b.authorize(ctx, true); err != nil {
				return err
			}
			continue
		default:
			return fmt.Errorf("b2 delete: %s: %s", res.Status, msg)
		}
	}
	return errExpired
}
