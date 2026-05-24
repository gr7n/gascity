package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	sessionAttachmentMaxBytes       = 15 << 20
	sessionAttachmentMultipartSlack = 1 << 20
)

type sessionAttachmentResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
}

func (sm *SupervisorMux) serveCitySessionAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if sm.readOnly {
		writeError(w, http.StatusForbidden, "read_only", "mutations disabled: server bound to non-localhost address")
		return
	}
	if r.Header.Get(csrfHeaderName) == "" {
		writeError(w, http.StatusForbidden, "csrf", "X-GC-Request header required on mutation endpoints")
		return
	}
	srv := sm.resolveCityServer(r.PathValue("cityName"))
	if srv == nil {
		problemCityNotFound.writeTo(w)
		return
	}
	srv.handleSessionAttachmentUpload(w, r, r.PathValue("cityName"), r.PathValue("id"))
}

func (sm *SupervisorMux) serveCitySessionAttachment(w http.ResponseWriter, r *http.Request) {
	srv := sm.resolveCityServer(r.PathValue("cityName"))
	if srv == nil {
		problemCityNotFound.writeTo(w)
		return
	}
	srv.handleSessionAttachmentServe(w, r, r.PathValue("id"), r.PathValue("attachmentID"), r.PathValue("filename"))
}

func (s *Server) handleSessionAttachmentUpload(w http.ResponseWriter, r *http.Request, cityName, idRef string) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}
	sessionID, err := s.resolveSessionIDAllowClosedWithConfig(store, idRef)
	if err != nil {
		writeHumaStatusError(w, humaResolveError(err))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, sessionAttachmentMaxBytes+sessionAttachmentMultipartSlack)
	if err := r.ParseMultipartForm(sessionAttachmentMultipartSlack); err != nil {
		if strings.Contains(err.Error(), "request body too large") || strings.Contains(err.Error(), "multipart: NextPart: EOF") {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("image uploads are limited to %d MB", sessionAttachmentMaxBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_multipart", err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", "multipart field 'file' is required")
		return
	}
	defer func() { _ = file.Close() }()

	resp, err := s.storeSessionAttachment(cityName, sessionID, file, header)
	if err != nil {
		writeSessionAttachmentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleSessionAttachmentServe(w http.ResponseWriter, r *http.Request, idRef, attachmentID, filename string) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}
	sessionID, err := s.resolveSessionIDAllowClosedWithConfig(store, idRef)
	if err != nil {
		writeHumaStatusError(w, humaResolveError(err))
		return
	}

	attachmentID = strings.TrimSpace(attachmentID)
	if !isSafeAttachmentID(attachmentID) {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	filename = sanitizeAttachmentFilename(filename, "")
	if filename == "" {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	path := filepath.Join(sessionAttachmentRoot(s.state.CityPath()), safeAttachmentPathPart(sessionID), attachmentID, filename)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	w.Header().Set("Content-Disposition", "inline; filename="+strconvQuote(filename))
	http.ServeFile(w, r, path)
}

func (s *Server) storeSessionAttachment(cityName, sessionID string, file multipart.File, header *multipart.FileHeader) (sessionAttachmentResponse, error) {
	if header == nil {
		return sessionAttachmentResponse{}, sessionAttachmentClientError{status: http.StatusBadRequest, code: "missing_file", message: "multipart field 'file' is required"}
	}
	if header.Size > sessionAttachmentMaxBytes {
		return sessionAttachmentResponse{}, sessionAttachmentClientError{status: http.StatusRequestEntityTooLarge, code: "too_large", message: fmt.Sprintf("image uploads are limited to %d MB", sessionAttachmentMaxBytes>>20)}
	}

	peek := make([]byte, 512)
	n, readErr := file.Read(peek)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return sessionAttachmentResponse{}, readErr
	}
	mimeType := imageMimeType(header, peek[:n])
	if mimeType == "" {
		return sessionAttachmentResponse{}, sessionAttachmentClientError{status: http.StatusUnsupportedMediaType, code: "unsupported_media_type", message: "only image attachments are supported"}
	}

	attachmentID, err := newSessionAttachmentID()
	if err != nil {
		return sessionAttachmentResponse{}, err
	}
	filename := sanitizeAttachmentFilename(header.Filename, extensionForImageMime(mimeType))
	if filename == "" {
		filename = "image" + extensionForImageMime(mimeType)
	}

	dir := filepath.Join(sessionAttachmentRoot(s.state.CityPath()), safeAttachmentPathPart(sessionID), attachmentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return sessionAttachmentResponse{}, err
	}
	path := filepath.Join(dir, filename)
	tmpPath := path + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sessionAttachmentResponse{}, err
	}
	written, copyErr := writeLimitedAttachment(out, peek[:n], file)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return sessionAttachmentResponse{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return sessionAttachmentResponse{}, closeErr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return sessionAttachmentResponse{}, err
	}

	relativeURL := sessionAttachmentURL(cityName, sessionID, attachmentID, filename)
	return sessionAttachmentResponse{
		ID:       attachmentID,
		Name:     filename,
		MimeType: mimeType,
		Path:     path,
		Size:     written,
		URL:      relativeURL,
	}, nil
}

func writeLimitedAttachment(out io.Writer, first []byte, rest io.Reader) (int64, error) {
	var written int64
	if len(first) > 0 {
		n, err := out.Write(first)
		written += int64(n)
		if err != nil {
			return written, err
		}
	}
	limit := sessionAttachmentMaxBytes - written
	if limit < 0 {
		return written, sessionAttachmentClientError{status: http.StatusRequestEntityTooLarge, code: "too_large", message: fmt.Sprintf("image uploads are limited to %d MB", sessionAttachmentMaxBytes>>20)}
	}
	lr := &io.LimitedReader{R: rest, N: limit + 1}
	n, err := io.Copy(out, lr)
	written += n
	if err != nil {
		return written, err
	}
	if lr.N == 0 {
		return written, sessionAttachmentClientError{status: http.StatusRequestEntityTooLarge, code: "too_large", message: fmt.Sprintf("image uploads are limited to %d MB", sessionAttachmentMaxBytes>>20)}
	}
	return written, nil
}

func imageMimeType(header *multipart.FileHeader, sample []byte) string {
	declared := strings.ToLower(strings.TrimSpace(header.Header.Get("Content-Type")))
	if isAllowedImageMime(declared) {
		return declared
	}
	detected := strings.ToLower(http.DetectContentType(sample))
	if isAllowedImageMime(detected) {
		return detected
	}
	return ""
}

func isAllowedImageMime(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func extensionForImageMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

func newSessionAttachmentID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func isSafeAttachmentID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, ch := range id {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func sessionAttachmentRoot(cityPath string) string {
	return filepath.Join(cityPath, ".gc", "dashboard", "attachments")
}

func sessionAttachmentURL(cityName, sessionID, attachmentID, filename string) string {
	return "/v0/city/" + url.PathEscape(cityName) +
		"/session/" + url.PathEscape(sessionID) +
		"/attachments/" + url.PathEscape(attachmentID) +
		"/" + url.PathEscape(filename)
}

func safeAttachmentPathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	var b strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			b.WriteRune(ch)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "_"
	}
	return out
}

func sanitizeAttachmentFilename(name, fallbackExt string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.Trim(base, ". ")
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, ch := range base {
		switch {
		case ch == '.' || ch == '_' || ch == '-':
			b.WriteRune(ch)
			lastDash = false
		case ch == ' ':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		case unicode.IsLetter(ch) || unicode.IsDigit(ch):
			b.WriteRune(ch)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		return ""
	}
	if filepath.Ext(out) == "" && fallbackExt != "" {
		out += fallbackExt
	}
	if len(out) > 160 {
		ext := filepath.Ext(out)
		stem := strings.TrimSuffix(out, ext)
		maxStem := 160 - len(ext)
		if maxStem < 1 {
			maxStem = 1
		}
		if len(stem) > maxStem {
			stem = stem[:maxStem]
		}
		out = stem + ext
	}
	return out
}

type sessionAttachmentClientError struct {
	status  int
	code    string
	message string
}

func (e sessionAttachmentClientError) Error() string {
	return e.message
}

func writeSessionAttachmentError(w http.ResponseWriter, err error) {
	var clientErr sessionAttachmentClientError
	if errors.As(err, &clientErr) {
		writeError(w, clientErr.status, clientErr.code, clientErr.message)
		return
	}
	if errors.Is(err, beads.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", err.Error())
}

func strconvQuote(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
