package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandleSessionAttachmentUploadStoresImageAndServesIt(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "With Image")

	body, contentType := multipartBody(t, "screen shot.png", "image/png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...))
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/attachments", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var resp sessionAttachmentResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("missing attachment id")
	}
	if resp.Name != "screen-shot.png" {
		t.Fatalf("Name = %q, want %q", resp.Name, "screen-shot.png")
	}
	if resp.MimeType != "image/png" {
		t.Fatalf("MimeType = %q, want image/png", resp.MimeType)
	}
	if !strings.Contains(resp.URL, "/attachments/"+resp.ID+"/screen-shot.png") {
		t.Fatalf("URL = %q, missing attachment path", resp.URL)
	}
	if !strings.Contains(resp.Path, ".gc/dashboard/attachments/") {
		t.Fatalf("Path = %q, want dashboard attachment path", resp.Path)
	}
	if data, err := os.ReadFile(resp.Path); err != nil {
		t.Fatalf("read stored attachment: %v", err)
	} else if !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("stored attachment did not preserve PNG bytes")
	}
	if resp.Status != "draft" {
		t.Fatalf("Status = %q, want draft", resp.Status)
	}
	manifest, err := readSessionAttachmentManifest(filepath.Dir(resp.Path))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Status != "draft" || manifest.ID != resp.ID || manifest.Name != resp.Name {
		t.Fatalf("manifest = %#v, want draft metadata for response", manifest)
	}

	getReq := httptest.NewRequest(http.MethodGet, resp.URL, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("serve status = %d, want %d; body: %s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	if got := getRec.Header().Get("Content-Disposition"); !strings.Contains(got, "screen-shot.png") {
		t.Fatalf("Content-Disposition = %q, want filename", got)
	}
	if !bytes.HasPrefix(getRec.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("served attachment did not preserve PNG bytes")
	}
}

func TestHandleSessionAttachmentDeleteRemovesDraft(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Delete Image")
	resp := uploadTestSessionAttachment(t, h, fs, info.ID)

	req := httptest.NewRequest(http.MethodDelete, sessionAttachmentDeleteURL(fs.CityName(), info.ID, resp.ID), nil)
	req.Header.Set(csrfHeaderName, "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(resp.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attachment dir still exists or unexpected error: %v", err)
	}
	getReq := httptest.NewRequest(http.MethodGet, resp.URL, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("serve status after delete = %d, want %d", getRec.Code, http.StatusNotFound)
	}
}

func TestHandleSessionSubmitMarksReferencedAttachmentsSent(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Send Image")
	resp := uploadTestSessionAttachment(t, h, fs, info.ID)

	body := `{"message":"Please inspect ` + resp.URL + `"}`
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted: %v", err)
	}
	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}
	manifest, err := readSessionAttachmentManifest(filepath.Dir(resp.Path))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Status != "sent" {
		t.Fatalf("manifest.Status = %q, want sent", manifest.Status)
	}
}

func TestHandleSessionAttachmentUploadPrunesOldDraftsAcrossSessions(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	source := createTestSession(t, fs.cityBeadStore, fs.sp, "Source Image")
	other := createTestSession(t, fs.cityBeadStore, fs.sp, "Other Image")
	old := time.Now().UTC().Add(-sessionAttachmentDraftTTL - time.Minute)

	oldDraftDir := writeTestSessionAttachmentDir(t, fs, other.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "old-draft.png", "draft", old)
	oldSentDir := writeTestSessionAttachmentDir(t, fs, other.ID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "old-sent.png", "sent", old)

	body, contentType := multipartBody(t, "fresh.png", "image/png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...))
	req := newPostRequest(cityURL(fs, "/session/")+source.ID+"/attachments", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if _, err := os.Stat(oldDraftDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old draft dir still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(oldSentDir); err != nil {
		t.Fatalf("old sent dir should be retained: %v", err)
	}
}

func TestHandleSessionAttachmentUploadPrunesOldUploadLeftovers(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	source := createTestSession(t, fs.cityBeadStore, fs.sp, "Source Image")
	other := createTestSession(t, fs.cityBeadStore, fs.sp, "Other Image")
	old := time.Now().UTC().Add(-sessionAttachmentTempTTL - time.Minute)

	leftoverID := "cccccccccccccccccccccccccccccccc"
	leftoverDir := filepath.Join(sessionAttachmentRoot(fs.CityPath()), safeAttachmentPathPart(other.ID), leftoverID)
	if err := os.MkdirAll(leftoverDir, 0o700); err != nil {
		t.Fatalf("mkdir leftover dir: %v", err)
	}
	leftoverTmp := filepath.Join(leftoverDir, "fresh.png.tmp")
	if err := os.WriteFile(leftoverTmp, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write leftover tmp: %v", err)
	}
	if err := os.Chtimes(leftoverTmp, old, old); err != nil {
		t.Fatalf("chtimes leftover tmp: %v", err)
	}
	if err := os.Chtimes(leftoverDir, old, old); err != nil {
		t.Fatalf("chtimes leftover dir: %v", err)
	}

	body, contentType := multipartBody(t, "fresh.png", "image/png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...))
	req := newPostRequest(cityURL(fs, "/session/")+source.ID+"/attachments", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if _, err := os.Stat(leftoverDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover dir still exists or unexpected error: %v", err)
	}
}

func TestHandleSessionAttachmentUploadRejectsNonImage(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "No Text")

	body, contentType := multipartBody(t, "note.txt", "text/plain", []byte("plain text"))
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/attachments", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("upload status = %d, want %d; body: %s", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
}

func TestHandleSessionAttachmentUploadRejectsSpoofedImageContentType(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Spoofed Image")

	body, contentType := multipartBody(t, "screen.html", "image/png", []byte("<html><script>alert(1)</script></html>"))
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/attachments", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("upload status = %d, want %d; body: %s", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
}

func TestHandleSessionAttachmentServeRejectsStoredNonImage(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Stored Text")
	attachmentID := "0123456789abcdef0123456789abcdef"
	dir := filepath.Join(sessionAttachmentRoot(fs.CityPath()), safeAttachmentPathPart(info.ID), attachmentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir attachment dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "screen.png"), []byte("not actually an image"), 0o600); err != nil {
		t.Fatalf("write spoofed attachment: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, sessionAttachmentURL(fs.CityName(), info.ID, attachmentID, "screen.png"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("serve status = %d, want %d; body: %s", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
}

func TestHandleSessionAttachmentServeRejectsSymlinkEscape(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Symlink Image")
	attachmentID := "0123456789abcdef0123456789abcdef"
	dir := filepath.Join(sessionAttachmentRoot(fs.CityPath()), safeAttachmentPathPart(info.ID), attachmentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir attachment dir: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...), 0o600); err != nil {
		t.Fatalf("write outside image: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "screen.png")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, sessionAttachmentURL(fs.CityName(), info.ID, attachmentID, "screen.png"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("serve status = %d, want %d; body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func multipartBody(t *testing.T, filename, contentType string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	partHeader.Set("Content-Type", contentType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write multipart payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

func uploadTestSessionAttachment(t *testing.T, h http.Handler, fs *fakeState, sessionID string) sessionAttachmentResponse {
	t.Helper()
	body, contentType := multipartBody(t, "screen shot.png", "image/png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...))
	req := newPostRequest(cityURL(fs, "/session/")+sessionID+"/attachments", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var resp sessionAttachmentResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	return resp
}

func writeTestSessionAttachmentDir(t *testing.T, fs *fakeState, sessionID, attachmentID, filename, status string, timestamp time.Time) string {
	t.Helper()
	dir := filepath.Join(sessionAttachmentRoot(fs.CityPath()), safeAttachmentPathPart(sessionID), attachmentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir attachment dir: %v", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...), 0o600); err != nil {
		t.Fatalf("write attachment image: %v", err)
	}
	stamp := timestamp.Format(time.RFC3339Nano)
	if err := writeSessionAttachmentManifest(dir, sessionAttachmentManifest{
		ID:        attachmentID,
		Name:      filename,
		MimeType:  "image/png",
		Path:      path,
		Size:      72,
		Status:    status,
		CreatedAt: stamp,
		UpdatedAt: stamp,
	}); err != nil {
		t.Fatalf("write attachment manifest: %v", err)
	}
	return dir
}

func sessionAttachmentDeleteURL(cityName, sessionID, attachmentID string) string {
	return "/v0/city/" + cityName + "/session/" + sessionID + "/attachments/" + attachmentID
}
