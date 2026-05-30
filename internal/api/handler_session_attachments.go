package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	sessionAttachmentMaxBytes       = 15 << 20
	sessionAttachmentMultipartSlack = 1 << 20
	sessionAttachmentDraftTTL       = 24 * time.Hour
	sessionAttachmentTempTTL        = time.Hour
)

type sessionAttachmentResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Status   string `json:"status,omitempty"`
	URL      string `json:"url"`
}

type sessionAttachmentManifest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MimeType  string `json:"mime_type"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
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

func (sm *SupervisorMux) serveCitySessionAttachmentDelete(w http.ResponseWriter, r *http.Request) {
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
	srv.handleSessionAttachmentDelete(w, r, r.PathValue("id"), r.PathValue("attachmentID"))
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
	_ = s.pruneOldSessionAttachments(time.Now())

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

func (s *Server) handleSessionAttachmentDelete(w http.ResponseWriter, _ *http.Request, idRef, attachmentID string) {
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

	if err := s.deleteSessionAttachment(sessionID, attachmentID); err != nil {
		writeSessionAttachmentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	root, dir := s.sessionAttachmentDir(sessionID, attachmentID)
	path, err := resolveSessionAttachmentFilePath(root, dir, filename)
	if err != nil {
		writeSessionAssetError(w, err)
		return
	}
	if err := serveSessionAssetFile(w, r, path); err != nil {
		writeSessionAssetError(w, err)
		return
	}
}

func resolveSessionAttachmentFilePath(root, dir, filename string) (string, error) {
	if err := ensureAttachmentDirWithinRoot(root, dir); err != nil {
		return "", sessionAssetErrorFromAttachmentError(err)
	}
	dirEval, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", sessionAssetClientError{status: http.StatusNotFound, code: "not_found", message: "attachment not found"}
		}
		return "", err
	}
	path := filepath.Join(dir, filename)
	targetEval, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", sessionAssetClientError{status: http.StatusNotFound, code: "not_found", message: "attachment not found"}
		}
		return "", err
	}
	if !pathWithinDir(dirEval, targetEval) {
		return "", sessionAssetClientError{status: http.StatusForbidden, code: "forbidden", message: "attachment path escaped storage root"}
	}
	return targetEval, nil
}

func sessionAssetErrorFromAttachmentError(err error) error {
	var clientErr sessionAttachmentClientError
	if errors.As(err, &clientErr) {
		return sessionAssetClientError(clientErr)
	}
	return err
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	manifest := sessionAttachmentManifest{
		ID:        attachmentID,
		Name:      filename,
		MimeType:  mimeType,
		Path:      path,
		Size:      written,
		Status:    "draft",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeSessionAttachmentManifest(dir, manifest); err != nil {
		_ = os.RemoveAll(dir)
		return sessionAttachmentResponse{}, err
	}

	relativeURL := sessionAttachmentURL(cityName, sessionID, attachmentID, filename)
	return sessionAttachmentResponse{
		ID:       attachmentID,
		Name:     filename,
		MimeType: mimeType,
		Path:     path,
		Size:     written,
		Status:   "draft",
		URL:      relativeURL,
	}, nil
}

func (s *Server) deleteSessionAttachment(sessionID, attachmentID string) error {
	attachmentID = strings.TrimSpace(attachmentID)
	if !isSafeAttachmentID(attachmentID) {
		return sessionAttachmentClientError{status: http.StatusNotFound, code: "not_found", message: "attachment not found"}
	}
	root, dir := s.sessionAttachmentDir(sessionID, attachmentID)
	if err := ensureAttachmentDirWithinRoot(root, dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return nil
}

func (s *Server) markSubmittedSessionAttachmentsSent(sessionID, message string) error {
	ids := submittedAttachmentIDs(sessionID, message)
	for _, attachmentID := range ids {
		_, dir := s.sessionAttachmentDir(sessionID, attachmentID)
		manifest, err := readSessionAttachmentManifest(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if manifest.Status == "sent" {
			continue
		}
		manifest.Status = "sent"
		manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := writeSessionAttachmentManifest(dir, manifest); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) pruneOldSessionAttachments(now time.Time) error {
	root := sessionAttachmentRoot(s.state.CityPath())
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || safeAttachmentPathPart(entry.Name()) != entry.Name() {
			continue
		}
		sessionDir := filepath.Join(root, entry.Name())
		if err := s.pruneOldSessionAttachmentDir(entry.Name(), sessionDir, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) pruneOldSessionAttachmentDir(sessionID, sessionDir string, now time.Time) error {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !isSafeAttachmentID(entry.Name()) {
			continue
		}
		dir := filepath.Join(sessionDir, entry.Name())
		manifest, err := readSessionAttachmentManifest(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if err := pruneOldManifestlessSessionAttachmentDir(dir, now); err != nil {
					return err
				}
			}
			continue
		}
		if err := pruneOldSessionAttachmentTemps(dir, now); err != nil {
			return err
		}
		if manifest.Status != "draft" {
			continue
		}
		timestamp := manifest.UpdatedAt
		if timestamp == "" {
			timestamp = manifest.CreatedAt
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil || now.Sub(updatedAt) < sessionAttachmentDraftTTL {
			continue
		}
		if err := s.deleteSessionAttachment(sessionID, entry.Name()); err != nil && !isSessionAttachmentNotFound(err) {
			return err
		}
	}
	return nil
}

func pruneOldSessionAttachmentTemps(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if now.Sub(info.ModTime()) < sessionAttachmentTempTTL {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func pruneOldManifestlessSessionAttachmentDir(dir string, now time.Time) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || now.Sub(info.ModTime()) < sessionAttachmentTempTTL {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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

func imageMimeType(_ *multipart.FileHeader, sample []byte) string {
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

func (s *Server) sessionAttachmentDir(sessionID, attachmentID string) (string, string) {
	root := sessionAttachmentRoot(s.state.CityPath())
	dir := filepath.Join(root, safeAttachmentPathPart(sessionID), attachmentID)
	return root, dir
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

func ensureAttachmentDirWithinRoot(root, dir string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootEval, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionAttachmentClientError{status: http.StatusNotFound, code: "not_found", message: "attachment not found"}
		}
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionAttachmentClientError{status: http.StatusNotFound, code: "not_found", message: "attachment not found"}
		}
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return sessionAttachmentClientError{status: http.StatusNotFound, code: "not_found", message: "attachment not found"}
	}
	dirEval, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionAttachmentClientError{status: http.StatusNotFound, code: "not_found", message: "attachment not found"}
		}
		return err
	}
	if !pathWithinDir(rootEval, dirEval) {
		return sessionAttachmentClientError{status: http.StatusForbidden, code: "forbidden", message: "attachment path escaped storage root"}
	}
	return nil
}

func writeSessionAttachmentManifest(dir string, manifest sessionAttachmentManifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	path := sessionAttachmentManifestPath(dir)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func readSessionAttachmentManifest(dir string) (sessionAttachmentManifest, error) {
	payload, err := os.ReadFile(sessionAttachmentManifestPath(dir))
	if err != nil {
		return sessionAttachmentManifest{}, err
	}
	var manifest sessionAttachmentManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return sessionAttachmentManifest{}, err
	}
	return manifest, nil
}

func sessionAttachmentManifestPath(dir string) string {
	return filepath.Join(dir, ".manifest.json")
}

func isSessionAttachmentNotFound(err error) bool {
	var clientErr sessionAttachmentClientError
	return errors.As(err, &clientErr) && clientErr.status == http.StatusNotFound
}

var submittedAttachmentPattern = regexp.MustCompile(`/session/([^/\s]+)/attachments/([0-9a-f]{32})(?:/|$)`)

func submittedAttachmentIDs(sessionID, message string) []string {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, match := range submittedAttachmentPattern.FindAllStringSubmatch(message, -1) {
		if len(match) < 3 {
			continue
		}
		refSessionID, err := url.PathUnescape(match[1])
		if err != nil || refSessionID != sessionID {
			continue
		}
		attachmentID := match[2]
		if !isSafeAttachmentID(attachmentID) || seen[attachmentID] {
			continue
		}
		seen[attachmentID] = true
		ids = append(ids, attachmentID)
	}
	return ids
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
