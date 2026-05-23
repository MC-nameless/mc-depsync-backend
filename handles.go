package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// 创建新整合包
func handleCreateModpack(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(int)

	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	packID := uuid.New().String()
	_, err := db.Exec("INSERT INTO modpacks (id, owner_id, name) VALUES (?, ?, ?)", packID, userID, req.Name)
	if err != nil {
		http.Error(w, "Failed to create modpack", http.StatusInternalServerError)
		return
	}

	// 初始化目录
	os.MkdirAll(filepath.Join("./data/modpacks", packID, "mods"), 0755)
	os.MkdirAll(filepath.Join("./data/modpacks", packID, "manifests"), 0755)

	json.NewEncoder(w).Encode(map[string]string{"id": packID, "name": req.Name})
}

// 接收客户端上传的 Mod
func handleUploadMod(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(int)
	packID := r.PathValue("modpack_id")

	// 权限越权检查
	var ownerID int
	db.QueryRow("SELECT owner_id FROM modpacks WHERE id = ?", packID).Scan(&ownerID)
	if ownerID != userID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	r.ParseMultipartForm(50 << 20)
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Bad file upload", http.StatusBadRequest)
		return
	}
	defer file.Close()

	destPath := filepath.Join("./data/modpacks", packID, "mods", header.Filename)
	destFile, _ := os.Create(destPath)
	defer destFile.Close()
	io.Copy(destFile, file)

	w.Write([]byte(`{"status": "success"}`))
}

// 生成该整合包的新清单
func handleGenerateManifest(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(int)
	packID := r.PathValue("modpack_id")

	var ownerID int
	err := db.QueryRow("SELECT owner_id FROM modpacks WHERE id = ?", packID).Scan(&ownerID)
	if err != nil || ownerID != userID {
		http.Error(w, "Forbidden: You don't own this modpack", http.StatusForbidden)
		return
	}

	modsDir := filepath.Join("./data/modpacks", packID, "mods")
	manifestsDir := filepath.Join("./data/modpacks", packID, "manifests")

	var entries []FileEntry
	filepath.Walk(modsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(modsDir, path)
		relPath = filepath.ToSlash(filepath.Join("mods", relPath))

		f, _ := os.Open(path)
		h := sha256.New()
		io.Copy(h, f)
		f.Close()

		entries = append(entries, FileEntry{
			Path:   relPath,
			SHA256: hex.EncodeToString(h.Sum(nil)),
			Size:   info.Size(),
		})
		return nil
	})

	var currentVer int
	db.QueryRow("SELECT latest_version FROM modpacks WHERE id = ?", packID).Scan(&currentVer)
	newVer := currentVer + 1

	manifest := Manifest{
		Version:     newVer,
		LastUpdated: time.Now().UTC(),
		Files:       entries,
	}

	// 写入独立目录
	data, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(filepath.Join(manifestsDir, fmt.Sprintf("manifest_%d.json", newVer)), data, 0644)
	os.WriteFile(filepath.Join(manifestsDir, "latest.json"), data, 0644)

	// 更新数据库版本记录
	db.Exec("UPDATE modpacks SET latest_version = ? WHERE id = ?", newVer, packID)

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// 获取当前用户的所有整合包
func handleListModpacks(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(int)
	rows, err := db.Query("SELECT id, name, latest_version FROM modpacks WHERE owner_id = ?", userID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var packs []map[string]interface{}
	for rows.Next() {
		var id, name string
		var version int
		rows.Scan(&id, &name, &version)
		packs = append(packs, map[string]interface{}{"id": id, "name": name, "latest_version": version})
	}

	if packs == nil {
		packs = []map[string]interface{}{} // 防空
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(packs)
}

// 获取整合包内已上传的模组
func handleListMods(w http.ResponseWriter, r *http.Request) {
	packID := r.PathValue("modpack_id")

	modsDir := filepath.Join("./data/modpacks", packID, "mods")
	files, _ := os.ReadDir(modsDir)

	var fileNames []string
	for _, f := range files {
		if !f.IsDir() {
			fileNames = append(fileNames, f.Name())
		}
	}
	if fileNames == nil {
		fileNames = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fileNames)
}

// 删除特定模组
func handleDeleteMod(w http.ResponseWriter, r *http.Request) {
	packID := r.PathValue("modpack_id")
	filename := r.PathValue("filename")
	userID := r.Context().Value("userID").(int)

	var ownerID int
	err := db.QueryRow("SELECT owner_id FROM modpacks WHERE id = ?", packID).Scan(&ownerID)
	if err != nil || ownerID != userID {
		http.Error(w, "Forbidden: You don't own this modpack", http.StatusForbidden)
		return
	}

	targetPath := filepath.Join("./data/modpacks", packID, "mods", filename)
	os.Remove(targetPath)
	w.Write([]byte(`{"status": "deleted"}`))
}
