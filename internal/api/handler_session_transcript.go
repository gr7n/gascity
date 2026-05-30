package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

type sessionTranscriptResponse struct {
	ID         string                       `json:"id"`
	Template   string                       `json:"template"`
	Format     string                       `json:"format"`
	Turns      []outputTurn                 `json:"turns"`
	Pagination *worker.TranscriptPagination `json:"pagination,omitempty"`
}

type sessionRawTranscriptResponse struct {
	ID         string                       `json:"id"`
	Template   string                       `json:"template"`
	Format     string                       `json:"format"`
	Messages   []json.RawMessage            `json:"messages"`
	Pagination *worker.TranscriptPagination `json:"pagination,omitempty"`
}

func (s *Server) handleSessionTranscript(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}

	id, err := s.resolveSessionIDAllowClosedWithConfig(store, r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}

	catalog, err := s.workerSessionCatalog(store)
	if err != nil {
		writeSessionManagerError(w, err)
		return
	}
	info, err := catalog.Get(id)
	if err != nil {
		writeSessionManagerError(w, err)
		return
	}
	handle, err := s.workerHandleForSession(store, id)
	if err != nil {
		writeSessionManagerError(w, err)
		return
	}
	path, err := handle.TranscriptPath(r.Context())
	if err != nil && !errors.Is(err, worker.ErrHistoryUnavailable) {
		writeSessionManagerError(w, err)
		return
	}

	wantRaw := r.URL.Query().Get("format") == "raw"

	if path != "" {
		tail := 0
		if v := r.URL.Query().Get("tail"); v != "" {
			if n, convErr := strconv.Atoi(v); convErr == nil && n >= 0 {
				tail = n
			}
		}
		before := r.URL.Query().Get("before")
		after := r.URL.Query().Get("after")

		if before != "" && after != "" {
			writeError(w, http.StatusUnprocessableEntity, "invalid_params", "before and after are mutually exclusive")
			return
		}

		if wantRaw {
			transcript, err := handle.Transcript(r.Context(), worker.TranscriptRequest{
				TailCompactions: tail,
				BeforeEntryID:   before,
				AfterEntryID:    after,
				Raw:             true,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "reading session log: "+err.Error())
				return
			}
			writeJSON(w, http.StatusOK, sessionRawTranscriptResponse{
				ID:         info.ID,
				Template:   info.Template,
				Format:     "raw",
				Messages:   transcript.RawMessages,
				Pagination: transcript.Session.Pagination,
			})
			return
		}

		transcript, err := handle.Transcript(r.Context(), worker.TranscriptRequest{
			TailCompactions: tail,
			BeforeEntryID:   before,
			AfterEntryID:    after,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "reading session log: "+err.Error())
			return
		}
		sess := transcript.Session

		turns := make([]outputTurn, 0, len(sess.Messages))
		for _, entry := range sess.Messages {
			turn := entryToTurn(entry)
			if !outputTurnHasContent(turn) {
				continue
			}
			turns = appendOutputTurnDistinct(turns, turn)
		}
		if len(turns) == 0 && before == "" && after == "" {
			if peekTurns, ok, peekErr := s.peekSessionTranscriptTurns(r.Context(), info, handle); peekErr != nil {
				writeError(w, http.StatusInternalServerError, "internal", peekErr.Error())
				return
			} else if ok {
				writeJSON(w, http.StatusOK, sessionTranscriptResponse{
					ID:       info.ID,
					Template: info.Template,
					Format:   "text",
					Turns:    peekTurns,
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, sessionTranscriptResponse{
			ID:         info.ID,
			Template:   info.Template,
			Format:     "conversation",
			Turns:      turns,
			Pagination: sess.Pagination,
		})
		return
	}

	if wantRaw {
		writeJSON(w, http.StatusOK, sessionRawTranscriptResponse{
			ID:       info.ID,
			Template: info.Template,
			Format:   "raw",
			Messages: []json.RawMessage{},
		})
		return
	}

	turns, ok, peekErr := s.peekSessionTranscriptTurns(r.Context(), info, handle)
	if peekErr != nil {
		writeError(w, http.StatusInternalServerError, "internal", peekErr.Error())
		return
	}
	if ok {
		writeJSON(w, http.StatusOK, sessionTranscriptResponse{
			ID:       info.ID,
			Template: info.Template,
			Format:   "text",
			Turns:    turns,
		})
		return
	}

	writeJSON(w, http.StatusOK, sessionTranscriptResponse{
		ID:       info.ID,
		Template: info.Template,
		Format:   "conversation",
		Turns:    []outputTurn{},
	})
}

func (s *Server) peekSessionTranscriptTurns(ctx context.Context, info session.Info, handle worker.Handle) ([]outputTurn, bool, error) {
	output, err := handle.Peek(ctx, 100)
	if err != nil {
		if errors.Is(err, session.ErrSessionInactive) {
			return nil, false, nil
		}
		if info.State == session.StateActive && s.sessionProviderIsRunning(info.SessionName) {
			return nil, false, err
		}
		return nil, false, nil
	}
	turns := []outputTurn{}
	if output != "" {
		turns = append(turns, outputTurn{Role: "output", Text: output})
	}
	return turns, true, nil
}

func (s *Server) sessionProviderIsRunning(sessionName string) bool {
	if s == nil || s.state == nil || s.state.SessionProvider() == nil {
		return false
	}
	return s.state.SessionProvider().IsRunning(sessionName)
}
