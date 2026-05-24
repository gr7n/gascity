package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
)

func TestHandleSessionAttachmentUploadStoresImageAndServesIt(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "With Image")

	body, contentType := multipartBody(t, "file", "screen shot.png", "image/png", append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{0}, 64)...))
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

func TestHandleSessionAttachmentUploadRejectsNonImage(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	info := createTestSession(t, fs.cityBeadStore, fs.sp, "No Text")

	body, contentType := multipartBody(t, "file", "note.txt", "text/plain", []byte("plain text"))
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/attachments", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("upload status = %d, want %d; body: %s", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
}

func multipartBody(t *testing.T, fieldName, filename, contentType string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="`+fieldName+`"; filename="`+filename+`"`)
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
