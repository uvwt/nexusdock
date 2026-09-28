package httpx

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uvwt/nexusdock/internal/privatenotes"
)

func (s *Server) registerPrivateNoteRoutes(r chi.Router) {
	r.Post("/v1/private-notes/search", s.searchPrivateNotes)
	r.Post("/v1/private-notes/read", s.readPrivateNote)
	r.Post("/v1/private-notes/write", s.writePrivateNote)
	r.Post("/v1/private-notes/delete", s.deletePrivateNote)
	r.Post("/v1/private-notes/status", s.privateNoteStatus)
	r.Post("/v1/private-notes/maintenance", s.maintainPrivateNotes)
}

func (s *Server) searchPrivateNotes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.executePrivateNoteSearch(r.Context(), privateNoteSearchRequest{
		Query: req.Query, MaxResults: req.MaxResults,
	})
	if err != nil {
		writePrivateNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		privateNoteSearchResult
		OK     bool   `json:"ok"`
		Policy string `json:"policy"`
	}{
		privateNoteSearchResult: result,
		OK:                      true,
		Policy:                  "search only reads title, summary, tags, category, path, and updated_at; plaintext body is never searched or returned",
	})
}

func (s *Server) readPrivateNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.privateNotes.Read(req.Path, req.MaxBytes)
	if err != nil {
		writePrivateNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writePrivateNote(w http.ResponseWriter, r *http.Request) {
	var req privatenotes.WriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.privateNotes.Write(req)
	if err != nil {
		writePrivateNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deletePrivateNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string `json:"path"`
		Confirmed bool   `json:"confirmed"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.privateNotes.Delete(req.Path, req.Confirmed)
	if err != nil {
		writePrivateNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) privateNoteStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.privateNotes.Status(r.Context(), req.Action)
	if err != nil {
		writePrivateNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) maintainPrivateNotes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.privateNotes.Maintain(r.Context(), req.Action)
	if err != nil {
		writePrivateNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writePrivateNoteError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, privatenotes.ErrConfirmationRequired):
		status = http.StatusBadRequest
	case errors.Is(err, privatenotes.ErrNoteExists):
		status = http.StatusConflict
	case errors.Is(err, privatenotes.ErrNoteNotFound):
		status = http.StatusNotFound
	default:
		if code := privatenotes.ErrorCode(err); code != "PRIVATE_NOTE_OPERATION_FAILED" {
			status = http.StatusBadRequest
		}
	}
	writeError(w, status, privatenotes.ErrorCode(err), err.Error())
}
