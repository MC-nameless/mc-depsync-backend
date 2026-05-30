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

type ModInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

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

	var quota, used int64
	db.QueryRow("SELECT quota_bytes, used_bytes FROM users WHERE id = ?", ownerID).Scan(&quota, &used)
	if used+header.Size > quota {
		http.Error(w, "Quota exceeded: 空间不足", http.StatusForbidden)
		return
	}

	tempFileName := "temp_" + header.Filename
	tempPath := filepath.Join("./data/modpacks", packID, "mods", tempFileName)
	tempFile, _ := os.Create(tempPath)

	h := sha256.New()
	writer := io.MultiWriter(tempFile, h)
	io.Copy(writer, file)
	tempFile.Close()

	uploadedHash := hex.EncodeToString(h.Sum(nil))

	// 检测重复模组
	modsDir := filepath.Join("./data/modpacks", packID, "mods")
	if isDuplicateMod(modsDir, header.Size, uploadedHash, tempFileName) {
		os.Remove(tempPath)
		w.Write([]byte(`{"status": "skipped", "message": "File already exists"}`))
		return
	}

	// 保存文件并扣除空间
	finalPath := filepath.Join(modsDir, header.Filename)
	os.Rename(tempPath, finalPath)
	db.Exec("UPDATE users SET used_bytes = used_bytes + ? WHERE id = ?", header.Size, ownerID)

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

	var mods []ModInfo
	for _, f := range files {
		if !f.IsDir() {
			info, err := f.Info()
			if err == nil {
				mods = append(mods, ModInfo{Name: f.Name(), Size: info.Size()})
			}
		}
	}
	if mods == nil {
		mods = []ModInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mods)
}

// 重命名模组
func handleRenameMod(w http.ResponseWriter, r *http.Request) {
	packID := r.PathValue("modpack_id")
	userID := r.Context().Value("userID").(int)
	oldName := r.PathValue("filename")
	var req struct {
		NewName string `json:"new_name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var ownerID int
	err := db.QueryRow("SELECT owner_id FROM modpacks WHERE id = ?", packID).Scan(&ownerID)
	if err != nil || ownerID != userID {
		http.Error(w, "Forbidden: You don't own this modpack", http.StatusForbidden)
		return
	}

	oldPath := filepath.Join("./data/modpacks", packID, "mods", oldName)
	newPath := filepath.Join("./data/modpacks", packID, "mods", req.NewName)
	os.Rename(oldPath, newPath)

	w.Write([]byte(`{"status": "renamed"}`))
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
	info, err := os.Stat(targetPath)
	if err == nil {
		db.Exec("UPDATE users SET used_bytes = used_bytes - ? WHERE id = ?", info.Size(), ownerID)
	}
	os.Remove(targetPath)
	w.Write([]byte(`{"status": "deleted"}`))
}

// 检测重复模组
func isDuplicateMod(modsDir string, targetSize int64, targetHash string, tempFileName string) bool {
	files, _ := os.ReadDir(modsDir)
	for _, f := range files {
		if f.IsDir() || f.Name() == tempFileName {
			continue
		}
		info, _ := f.Info()
		if info.Size() != targetSize {
			continue
		}

		path := filepath.Join(modsDir, f.Name())
		file, _ := os.Open(path)
		h := sha256.New()
		io.Copy(h, file)
		file.Close()

		if hex.EncodeToString(h.Sum(nil)) == targetHash {
			return true
		}
	}
	return false
}

// 管理员接口：列出所有用户及其配额使用情况
func handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query("SELECT id, username, role, quota_bytes, used_bytes FROM users")
	defer rows.Close()

	var users []map[string]interface{}
	for rows.Next() {
		var id int
		var username, role string
		var quota, used int64
		rows.Scan(&id, &username, &role, &quota, &used)
		users = append(users, map[string]interface{}{
			"id": id, "username": username, "role": role,
			"quota_bytes": quota, "used_bytes": used,
		})
	}
	if users == nil {
		users = []map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

// 管理员接口：更新用户角色或配额
func handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	targetUserID := r.PathValue("user_id")
	var req struct {
		Role       string `json:"role"`
		QuotaBytes int64  `json:"quota_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	_, err := db.Exec("UPDATE users SET role = ?, quota_bytes = ? WHERE id = ?", req.Role, req.QuotaBytes, targetUserID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	w.Write([]byte(`{"status": "updated"}`))
}
