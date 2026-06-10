package server

import (
	"io"
	"net/http"
)

// GET /QuickConnect/Enabled — whether the QuickConnect login flow is offered.
// Disabled until implemented as a deliberate, audited feature.
func (s *Server) handleQuickConnectEnabled(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, false)
}

// GET /DisplayPreferences/{id} — per-client UI preferences. Clients request
// these early; returning a valid default object keeps them happy until
// persistence is implemented.
func (s *Server) handleGetDisplayPreferences(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.writeJSON(w, http.StatusOK, map[string]any{
		"Id":                 id,
		"SortBy":             "SortName",
		"SortOrder":          "Ascending",
		"RememberIndexing":   false,
		"RememberSorting":    false,
		"PrimaryImageHeight": 250,
		"PrimaryImageWidth":  250,
		"ScrollDirection":    "Horizontal",
		"ShowBackdrop":       true,
		"ShowSidebar":        false,
		"Client":             r.URL.Query().Get("client"),
		"CustomPrefs":        map[string]string{},
	})
}

// POST /DisplayPreferences/{id} — accept and discard preference updates for now.
func (s *Server) handleUpdateDisplayPreferences(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 64<<10))
	w.WriteHeader(http.StatusNoContent)
}
