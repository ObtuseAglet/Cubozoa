package server

import (
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
)

// GET /Branding/Configuration — login-screen branding. Clients fetch this
// before showing the login form.
func (s *Server) handleBrandingConfiguration(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, jellyfin.BrandingOptions{
		LoginDisclaimer:     "",
		CustomCss:           "",
		SplashscreenEnabled: false,
	})
}

// GET /Branding/Css(.css) — custom CSS. Empty for now, but must return valid
// text/css so clients that inject it do not error.
func (s *Server) handleBrandingCss(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}
