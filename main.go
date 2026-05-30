package main

import (
	"log"
	"net/http"
	"path/filepath"
	"time"
)

type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	Version     int         `json:"version"`
	LastUpdated time.Time   `json:"last_updated"`
	Files       []FileEntry `json:"files"`
}

func main() {
	initDB()
	defer db.Close()

	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/register", handleRegister)
	mux.HandleFunc("POST /api/auth/login", handleLogin)

	mux.Handle("GET /download/cunz/", http.StripPrefix("/download/cunz/", http.FileServer(http.Dir("./data/cunz"))))
	mux.HandleFunc("GET /api/modpacks/{modpack_id}/manifest/latest", func(w http.ResponseWriter, r *http.Request) {
		packID := r.PathValue("modpack_id")
		http.ServeFile(w, r, filepath.Join("./data/modpacks", packID, "manifests", "latest.json"))
	})

	mux.HandleFunc("POST /api/modpacks", AuthMiddleware(handleCreateModpack))
	mux.HandleFunc("POST /api/modpacks/{modpack_id}/upload", AuthMiddleware(handleUploadMod))
	mux.HandleFunc("POST /api/modpacks/{modpack_id}/manifest/generate", AuthMiddleware(handleGenerateManifest))
	mux.HandleFunc("PUT /api/modpacks/{modpack_id}/mods/{filename}", AuthMiddleware(handleRenameMod))
	mux.HandleFunc("GET /api/modpacks", AuthMiddleware(handleListModpacks))
	mux.HandleFunc("GET /api/modpacks/{modpack_id}/mods", AuthMiddleware(handleListMods))
	mux.HandleFunc("DELETE /api/modpacks/{modpack_id}/mods/{filename}", AuthMiddleware(handleDeleteMod))
	mux.HandleFunc("GET /api/admin/users", AdminMiddleware(handleAdminListUsers))
	mux.HandleFunc("PUT /api/admin/users/{user_id}", AdminMiddleware(handleAdminUpdateUser))

	handler := corsMiddleware(mux)

	log.Println("Backend running on :8080...")
	http.ListenAndServe(":8080", handler)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
