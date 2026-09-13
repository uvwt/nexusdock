package httpx

import (
	"errors"
	"net/http"

	"github.com/uvwt/nexusdock/internal/privatenotes"
)

func (s *Server) registerPrivateNoteRoutes(mux *http.ServeMux, protected func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /v1/private-notes/search", protected(s.searchPrivateNotes))
	mux.HandleFunc("POST /v1/private-notes/read", protected(s.readPrivateNote))
	mux.HandleFunc("POST /v1/private-notes/write", protected(s.writePrivateNote))
	mux.HandleFunc("POST /v1/private-notes/delete", protected(s.deletePrivateNote))
	mux.HandleFunc("POST /v1/private-notes/status", protected(s.privateNoteStatus))
	mux.HandleFunc("POST /v1/private-notes/maintenance", protected(s.maintainPrivateNotes))
}

func (s *Server) searchPrivateNotes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	results, err := s.privateNotes.Search(r.Context(), req.Query, req.MaxResults)
	if err != nil {
		writePrivateNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "action": "search", "query": req.Query, "root": s.privateNotes.Root(),
		"results": results, "count": len(results), "metadata_only": true,
		"policy": "search only reads title, summary, tags, category, path, and updated_at; plaintext body is never searched or returned",
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
