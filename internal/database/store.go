package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(filename string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY);
 INSERT OR IGNORE INTO schema_version VALUES(1);
 CREATE TABLE IF NOT EXISTS snapshots(cache_key TEXT PRIMARY KEY, payload BLOB NOT NULL, fetched_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS network_settings(id INTEGER PRIMARY KEY, payload BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS subtitles(hash TEXT NOT NULL,id TEXT NOT NULL,label TEXT NOT NULL,language TEXT NOT NULL,vtt BLOB NOT NULL,PRIMARY KEY(hash,id));
 CREATE TABLE IF NOT EXISTS auth_config(id INTEGER PRIMARY KEY, token TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS movies(provider_id INTEGER PRIMARY KEY, imdb_id TEXT NOT NULL, title TEXT NOT NULL, payload BLOB NOT NULL, updated_at INTEGER NOT NULL);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

type Subtitle struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Language string `json:"language"`
}

func (s *Store) SaveSubtitle(ctx context.Context, hash string, sub Subtitle, vtt []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO subtitles VALUES(?,?,?,?,?) ON CONFLICT(hash,id) DO UPDATE SET label=excluded.label,language=excluded.language,vtt=excluded.vtt`, hash, sub.ID, sub.Label, sub.Language, vtt)
	return err
}
func (s *Store) Subtitles(ctx context.Context, hash string) ([]Subtitle, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,label,language FROM subtitles WHERE hash=? ORDER BY label", hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subtitle{}
	for rows.Next() {
		var sub Subtitle
		if err = rows.Scan(&sub.ID, &sub.Label, &sub.Language); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}
func (s *Store) Subtitle(ctx context.Context, hash, id string) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT vtt FROM subtitles WHERE hash=? AND id=?", hash, id).Scan(&b)
	return b, err
}
func (s *Store) NetworkConfig(ctx context.Context) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM network_settings WHERE id=1").Scan(&b)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}
func (s *Store) SaveNetworkConfig(ctx context.Context, b []byte) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO network_settings VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", b)
	return err
}
func (s *Store) TorrentPort(ctx context.Context, fallback int) (int, error) {
	var port int
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='torrent_port'").Scan(&port)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	return port, err
}
func (s *Store) SetTorrentPort(ctx context.Context, port int) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO settings VALUES('torrent_port',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", port)
	return err
}
func (s *Store) Get(ctx context.Context, key string) ([]byte, time.Time, error) {
	var b []byte
	var t int64
	err := s.db.QueryRowContext(ctx, "SELECT payload,fetched_at FROM snapshots WHERE cache_key=?", key).Scan(&b, &t)
	return b, time.Unix(t, 0), err
}
func (s *Store) Put(ctx context.Context, key string, b []byte, t time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO snapshots VALUES(?,?,?) ON CONFLICT(cache_key) DO UPDATE SET payload=excluded.payload,fetched_at=excluded.fetched_at", key, b, t.Unix())
	return err
}
func (s *Store) Movie(ctx context.Context, id int, imdb, title string, b []byte) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO movies VALUES(?,?,?,?,?) ON CONFLICT(provider_id) DO UPDATE SET imdb_id=excluded.imdb_id,title=excluded.title,payload=excluded.payload,updated_at=excluded.updated_at", id, imdb, title, b, time.Now().Unix())
	return err
}

// Download records are independent of expiring provider snapshots.
func (s *Store) InitDownloads(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS downloads(hash TEXT PRIMARY KEY,payload BLOB NOT NULL); INSERT OR IGNORE INTO schema_version VALUES(2);`)
	return err
}
func (s *Store) SaveDownload(ctx context.Context, hash string, payload []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO downloads VALUES(?,?) ON CONFLICT(hash) DO UPDATE SET payload=excluded.payload`, hash, payload)
	return err
}
func (s *Store) Downloads(ctx context.Context) ([][]byte, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT payload FROM downloads ORDER BY hash")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) DeleteDownload(ctx context.Context, hash string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM downloads WHERE hash=?", hash); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM subtitles WHERE hash=?", hash)
	return err
}
func (s *Store) AuthToken(ctx context.Context, fallback string) (string, error) {
	var token string
	err := s.db.QueryRowContext(ctx, "SELECT token FROM auth_config WHERE id=1").Scan(&token)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	if token == "" {
		return fallback, nil
	}
	return token, nil
}
func (s *Store) SetAuthToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO auth_config VALUES(1,?) ON CONFLICT(id) DO UPDATE SET token=excluded.token", token)
	return err
}
