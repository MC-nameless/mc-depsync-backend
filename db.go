package main

import (
	"database/sql"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

func initDB() {
	os.MkdirAll("./data", 0755)
	os.MkdirAll("./data/cunz", 0755)
	var err error
	db, err = sql.Open("sqlite3", "./data/mc_depsync.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'user',
		quota_bytes INTEGER NOT NULL DEFAULT 1073741824, -- 1GB
		used_bytes INTEGER NOT NULL DEFAULT 0
	);
	
	CREATE TABLE IF NOT EXISTS modpacks (
		id TEXT PRIMARY KEY,
		owner_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		latest_version INTEGER DEFAULT 0,
		FOREIGN KEY(owner_id) REFERENCES users(id)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		log.Fatalf("Failed to initialize database schema: %v", err)
	}
}
