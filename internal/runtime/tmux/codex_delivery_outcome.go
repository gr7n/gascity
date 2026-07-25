package tmux

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

const (
	codexDeliverySegmentLimit = 1024 * 1024
	codexSessionMetaLineLimit = 64 * 1024
)

type codexDeliverySnapshot struct {
	path   string
	offset int64
}

type codexDeliveryObservation struct {
	Accepted      bool
	ProviderError error
}

type codexDeliveryEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexDeliveryPayload struct {
	Type       string          `json:"type"`
	TurnID     string          `json:"turn_id"`
	Message    string          `json:"message"`
	Error      json.RawMessage `json:"error"`
	RateLimits json.RawMessage `json:"rate_limits"`
}

type codexRateLimitEvidence struct {
	LimitID   string
	LimitName string
	ResetsAt  int64
}

type codexTaskError struct {
	Message           string      `json:"message"`
	RetryAfter        json.Number `json:"retry_after"`
	RetryAfterSeconds json.Number `json:"retry_after_seconds"`
	RetryAfterMS      json.Number `json:"retry_after_ms"`
	CodexErrorInfo    struct {
		ResponseTooManyFailedAttempts codexTooManyError `json:"response_too_many_failed_attempts"`
	} `json:"codex_error_info"`
}

type codexTooManyError struct {
	HTTPStatusCode    int         `json:"http_status_code"`
	RetryAfter        json.Number `json:"retry_after"`
	RetryAfterSeconds json.Number `json:"retry_after_seconds"`
	RetryAfterMS      json.Number `json:"retry_after_ms"`
}

// snapshotCodexDelivery binds a future delivery receipt to the currently
// running pane's exact work directory and rollout byte boundary. We never use a
// later "latest transcript" lookup after sending because that could attribute a
// different same-account session's turn to this nudge.
func (t *Tmux) snapshotCodexDelivery(session, target string) (codexDeliverySnapshot, error) {
	codexHome, err := t.GetEnvironment(session, "CODEX_HOME")
	if err != nil {
		return codexDeliverySnapshot{}, fmt.Errorf("reading CODEX_HOME: %w", err)
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return codexDeliverySnapshot{}, errors.New("CODEX_HOME is empty")
	}

	workDir, err := t.targetPaneWorkDir(target)
	if err != nil {
		return codexDeliverySnapshot{}, fmt.Errorf("reading pane workdir: %w", err)
	}
	path, err := latestCodexTranscriptForWorkDir(filepath.Join(codexHome, "sessions"), workDir)
	if err != nil {
		return codexDeliverySnapshot{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return codexDeliverySnapshot{}, fmt.Errorf("stating codex rollout: %w", err)
	}
	return codexDeliverySnapshot{path: path, offset: info.Size()}, nil
}

func (t *Tmux) targetPaneWorkDir(target string) (string, error) {
	out, err := t.run("display-message", "-t", target, "-p", "#{pane_current_path}")
	if err != nil {
		return "", err
	}
	workDir := strings.TrimSpace(out)
	if workDir == "" {
		return "", errors.New("pane workdir is empty")
	}
	return filepath.Clean(workDir), nil
}

func latestCodexTranscriptForWorkDir(root, workDir string) (string, error) {
	workDir = filepath.Clean(strings.TrimSpace(workDir))
	if workDir == "." || workDir == "" {
		return "", errors.New("codex rollout workdir is empty")
	}

	var latestPath string
	var latestMod time.Time
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		candidateWorkDir, err := codexRolloutWorkDir(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if filepath.Clean(candidateWorkDir) != workDir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case latestPath == "", info.ModTime().After(latestMod):
			latestPath = path
			latestMod = info.ModTime()
		case info.ModTime().Equal(latestMod) && path != latestPath:
			return fmt.Errorf("ambiguous codex rollouts for workdir %q", workDir)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("locating codex rollout: %w", err)
	}
	if latestPath == "" {
		return "", fmt.Errorf("no codex rollout for workdir %q", workDir)
	}
	return latestPath, nil
}

func codexRolloutWorkDir(path string) (_ string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close() //nolint:errcheck // read-only metadata probe

	reader := bufio.NewReader(io.LimitReader(file, codexSessionMetaLineLimit+1))
	line, readErr := reader.ReadBytes('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", readErr
	}
	if len(line) > codexSessionMetaLineLimit {
		return "", errors.New("codex session_meta exceeds limit")
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			CWD string `json:"cwd"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &meta); err != nil || meta.Type != "session_meta" {
		return "", nil
	}
	return strings.TrimSpace(meta.Payload.CWD), nil
}

func (snapshot codexDeliverySnapshot) observe(message string) (codexDeliveryObservation, error) {
	file, err := os.Open(snapshot.path)
	if err != nil {
		return codexDeliveryObservation{}, err
	}
	defer file.Close() //nolint:errcheck // read-only receipt

	info, err := file.Stat()
	if err != nil {
		return codexDeliveryObservation{}, err
	}
	if info.Size() < snapshot.offset {
		return codexDeliveryObservation{}, errors.New("codex rollout shrank after delivery snapshot")
	}
	if info.Size()-snapshot.offset > codexDeliverySegmentLimit {
		return codexDeliveryObservation{}, errors.New("codex delivery segment exceeds limit")
	}
	if _, err := file.Seek(snapshot.offset, io.SeekStart); err != nil {
		return codexDeliveryObservation{}, err
	}
	segment, err := io.ReadAll(io.LimitReader(file, codexDeliverySegmentLimit+1))
	if err != nil {
		return codexDeliveryObservation{}, err
	}
	if len(segment) > codexDeliverySegmentLimit {
		return codexDeliveryObservation{}, errors.New("codex delivery segment exceeds limit")
	}
	return inspectCodexDeliverySegment(segment, message), nil
}

func inspectCodexDeliverySegment(segment []byte, message string) codexDeliveryObservation {
	var observation codexDeliveryObservation
	var taskStarted bool
	var startedTurn string
	var acceptedTurn string
	var rateLimit codexRateLimitEvidence

	for _, rawLine := range bytes.Split(segment, []byte{'\n'}) {
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			continue
		}
		var envelope codexDeliveryEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil || envelope.Type != "event_msg" {
			continue
		}
		var payload codexDeliveryPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			continue
		}
		switch payload.Type {
		case "task_started":
			taskStarted = true
			startedTurn = payload.TurnID
		case "user_message":
			if taskStarted {
				if payload.Message == message {
					observation.Accepted = true
					acceptedTurn = startedTurn
				}
			}
		case "token_count":
			if observation.Accepted {
				rateLimit = parseCodexRateLimitEvidence(payload.RateLimits)
			}
		case "task_complete":
			if !observation.Accepted || !sameCodexTurn(acceptedTurn, payload.TurnID) {
				if taskStarted && !observation.Accepted && sameCodexTurn(startedTurn, payload.TurnID) {
					taskStarted = false
					startedTurn = ""
				}
				continue
			}
			if providerErr := parseCodexProviderError(payload.Error, rateLimit); providerErr != nil {
				observation.ProviderError = providerErr
			}
		}
	}
	return observation
}

func sameCodexTurn(accepted, completed string) bool {
	return accepted == "" || completed == "" || accepted == completed
}

func parseCodexRateLimitEvidence(raw json.RawMessage) codexRateLimitEvidence {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return codexRateLimitEvidence{}
	}
	var limits struct {
		LimitID   string  `json:"limit_id"`
		LimitName *string `json:"limit_name"`
		Primary   struct {
			ResetsAt int64 `json:"resets_at"`
		} `json:"primary"`
	}
	if err := json.Unmarshal(raw, &limits); err != nil {
		return codexRateLimitEvidence{}
	}
	var limitName string
	if limits.LimitName != nil {
		limitName = strings.TrimSpace(*limits.LimitName)
	}
	return codexRateLimitEvidence{
		LimitID:   strings.TrimSpace(limits.LimitID),
		LimitName: limitName,
		ResetsAt:  limits.Primary.ResetsAt,
	}
}

func parseCodexProviderError(raw json.RawMessage, limit codexRateLimitEvidence) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte(`""`)) {
		return nil
	}

	var taskError codexTaskError
	if err := json.Unmarshal(trimmed, &taskError); err != nil {
		var message string
		if json.Unmarshal(trimmed, &message) != nil {
			return nil
		}
		taskError.Message = message
	}
	reason := boundedProviderReason(taskError.Message)
	tooMany := taskError.CodexErrorInfo.ResponseTooManyFailedAttempts
	status := tooMany.HTTPStatusCode
	if status == 0 && strings.Contains(reason, "429") {
		status = 429
	}
	retryAfter := firstRetryAfter(taskError, tooMany)
	if !codexCapacityFailure(reason, status, retryAfter) {
		return nil
	}
	return &runtime.ProviderUnavailableError{
		StatusCode: status,
		RetryAfter: retryAfter,
		LimitID:    limit.LimitID,
		LimitName:  limit.LimitName,
		ResetsAt:   limit.ResetsAt,
		Reason:     reason,
	}
}

func firstRetryAfter(taskError codexTaskError, tooMany codexTooManyError) string {
	for _, candidate := range []struct {
		value  json.Number
		suffix string
	}{
		{taskError.RetryAfterSeconds, "s"},
		{taskError.RetryAfterMS, "ms"},
		{taskError.RetryAfter, "s"},
		{tooMany.RetryAfterSeconds, "s"},
		{tooMany.RetryAfterMS, "ms"},
		{tooMany.RetryAfter, "s"},
	} {
		if candidate.value != "" {
			return candidate.value.String() + candidate.suffix
		}
	}
	return ""
}

func codexCapacityFailure(reason string, status int, retryAfter string) bool {
	if status == 429 || retryAfter != "" {
		return true
	}
	reason = strings.ToLower(reason)
	for _, marker := range []string{
		"too many requests",
		"rate limit",
		"usage limit",
		"quota",
		"capacity",
		"spend control",
		"insufficient credit",
	} {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}

func boundedProviderReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	const maxReason = 240
	if len(reason) > maxReason {
		return reason[:maxReason] + "…"
	}
	return reason
}

// submitCodexEnterAndConfirm keeps Codex submission exactly-once. Pane-busy is
// a fast success witness; if a turn completes between polls, the exact rollout
// segment decides accepted, provider-unavailable, or still ambiguous.
func submitCodexEnterAndConfirm(
	sendEnter func() error,
	wake func(),
	busy func() (bool, error),
	observe func() (codexDeliveryObservation, error),
	sleep func(time.Duration),
) error {
	confirmed, err := submitEnterAndConfirmLimit(sendEnter, wake, busy, sleep, 1)
	if err != nil {
		return err
	}
	if confirmed {
		return nil
	}
	observation, observeErr := observe()
	if observeErr != nil || !observation.Accepted {
		return runtime.ErrDeliveryUnconfirmed
	}
	if observation.ProviderError != nil {
		return observation.ProviderError
	}
	return nil
}
