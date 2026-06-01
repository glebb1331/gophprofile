package handlers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(api *API, staticDir string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60_000_000_000))
	r.Use(corsMiddleware)

	r.Get("/health", api.Health)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/avatars", api.UploadAvatar)
		r.Get("/avatars/{id}", api.GetAvatar)
		r.Get("/avatars/{id}/metadata", api.GetAvatarMetadata)
		r.Delete("/avatars/{id}", api.DeleteAvatar)
		r.Get("/users/{user_id}/avatar", api.GetUserAvatar)
		r.Get("/users/{user_id}/avatars", api.ListUserAvatars)
		r.Delete("/users/{user_id}/avatar", api.DeleteUserAvatar)
	})

	if staticDir != "" {
		fs := http.FileServer(http.Dir(staticDir))
		r.Get("/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.ServeFile(w, req, staticDir+"/index.html")
		}).ServeHTTP)
		r.Get("/web/upload", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.ServeFile(w, req, staticDir+"/index.html")
		}).ServeHTTP)
		r.Get("/web/gallery/{user_id}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.ServeFile(w, req, staticDir+"/gallery.html")
		}).ServeHTTP)
		r.Handle("/static/*", http.StripPrefix("/static/", fs))
	}

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", strings.Join([]string{
			"Content-Type", "X-User-ID", "If-None-Match",
		}, ", "))
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
