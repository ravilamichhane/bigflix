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
 CREATE TABLE IF NOT EXISTS subtitles(hash TEXT NOT NULL, file_index INTEGER NOT NULL DEFAULT -1, id TEXT NOT NULL, label TEXT NOT NULL, language TEXT NOT NULL, vtt BLOB NOT NULL, PRIMARY KEY(hash, file_index, id));
 CREATE TABLE IF NOT EXISTS auth_config(id INTEGER PRIMARY KEY, token TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS movies(provider_id INTEGER PRIMARY KEY, imdb_id TEXT NOT NULL, title TEXT NOT NULL, payload BLOB NOT NULL, updated_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS app_config(key TEXT PRIMARY KEY, payload BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS watch_progress(
   media_key TEXT PRIMARY KEY,
   media_type TEXT NOT NULL,
   tmdb_id INTEGER NOT NULL,
   season INTEGER NOT NULL DEFAULT 0,
   episode INTEGER NOT NULL DEFAULT 0,
   title TEXT NOT NULL,
   episode_title TEXT NOT NULL DEFAULT '',
   poster TEXT NOT NULL DEFAULT '',
   backdrop TEXT NOT NULL DEFAULT '',
   last_hash TEXT NOT NULL DEFAULT '',
   last_file_idx INTEGER NOT NULL DEFAULT -1,
   position REAL NOT NULL DEFAULT 0,
   duration REAL NOT NULL DEFAULT 0,
   completed INTEGER NOT NULL DEFAULT 0,
   updated_at INTEGER NOT NULL
 );
 CREATE INDEX IF NOT EXISTS idx_watch_progress_updated ON watch_progress(updated_at DESC);
 CREATE INDEX IF NOT EXISTS idx_watch_progress_series ON watch_progress(media_type, tmdb_id, season);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	s := &Store{db}
	if err := s.migrateSubtitles(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate subtitles: %w", err)
	}
	_, _ = db.Exec(`INSERT OR IGNORE INTO schema_version VALUES(3);`)
	return s, nil
}

func (s *Store) migrateSubtitles() error {
	rows, err := s.db.Query("PRAGMA table_info(subtitles)")
	if err != nil {
		return err
	}
	hasFileIndex := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "file_index" {
			hasFileIndex = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if hasFileIndex {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		CREATE TABLE subtitles_v2(
			hash TEXT NOT NULL,
			file_index INTEGER NOT NULL DEFAULT -1,
			id TEXT NOT NULL,
			label TEXT NOT NULL,
			language TEXT NOT NULL,
			vtt BLOB NOT NULL,
			PRIMARY KEY(hash, file_index, id)
		);
		INSERT INTO subtitles_v2(hash, file_index, id, label, language, vtt)
			SELECT hash, -1, id, label, language, vtt FROM subtitles;
		DROP TABLE subtitles;
		ALTER TABLE subtitles_v2 RENAME TO subtitles;
	`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

type Subtitle struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Language string `json:"language"`
}

func (s *Store) SaveSubtitle(ctx context.Context, hash string, sub Subtitle, vtt []byte) error {
	return s.SaveSubtitleForFile(ctx, hash, -1, sub, vtt)
}

func (s *Store) SaveSubtitleForFile(ctx context.Context, hash string, fileIndex int, sub Subtitle, vtt []byte) error {
	if fileIndex < -1 {
		fileIndex = -1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO subtitles(hash,file_index,id,label,language,vtt) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(hash,file_index,id) DO UPDATE SET label=excluded.label,language=excluded.language,vtt=excluded.vtt`,
		hash, fileIndex, sub.ID, sub.Label, sub.Language, vtt)
	return err
}

func (s *Store) Subtitles(ctx context.Context, hash string) ([]Subtitle, error) {
	return s.SubtitlesForFile(ctx, hash, -1)
}

func (s *Store) SubtitlesForFile(ctx context.Context, hash string, fileIndex int) ([]Subtitle, error) {
	var rows *sql.Rows
	var err error
	if fileIndex >= 0 {
		// Return tracks scoped to this episode file plus any legacy (-1) tracks saved for single-file torrents.
		rows, err = s.db.QueryContext(ctx,
			"SELECT id,label,language FROM subtitles WHERE hash=? AND (file_index=? OR file_index=-1) ORDER BY label",
			hash, fileIndex)
	} else {
		rows, err = s.db.QueryContext(ctx,
			"SELECT id,label,language FROM subtitles WHERE hash=? ORDER BY label", hash)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subtitle{}
	seen := map[string]bool{}
	for rows.Next() {
		var sub Subtitle
		if err = rows.Scan(&sub.ID, &sub.Label, &sub.Language); err != nil {
			return nil, err
		}
		if !seen[sub.ID] {
			seen[sub.ID] = true
			out = append(out, sub)
		}
	}
	return out, rows.Err()
}

func (s *Store) Subtitle(ctx context.Context, hash, id string) ([]byte, error) {
	return s.SubtitleForFile(ctx, hash, -1, id)
}

func (s *Store) SubtitleForFile(ctx context.Context, hash string, fileIndex int, id string) ([]byte, error) {
	var b []byte
	var err error
	if fileIndex >= 0 {
		err = s.db.QueryRowContext(ctx,
			"SELECT vtt FROM subtitles WHERE hash=? AND file_index=? AND id=?",
			hash, fileIndex, id).Scan(&b)
		if err == sql.ErrNoRows {
			err = s.db.QueryRowContext(ctx,
				"SELECT vtt FROM subtitles WHERE hash=? AND file_index=-1 AND id=?",
				hash, id).Scan(&b)
		}
		return b, err
	}
	err = s.db.QueryRowContext(ctx,
		"SELECT vtt FROM subtitles WHERE hash=? AND id=? ORDER BY file_index ASC LIMIT 1",
		hash, id).Scan(&b)
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

func (s *Store) DeleteSnapshotPrefix(ctx context.Context, prefix string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM snapshots WHERE cache_key LIKE ?", prefix+"%")
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

// Watch progress tracking (decoupled from transient torrent downloads).
func (s *Store) UpsertProgress(ctx context.Context, p WatchProgress) error {
	if p.MediaKey == "" {
		p.MediaKey = MediaKey(p.MediaType, p.TMDBID, p.Season, p.Episode)
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = time.Now().Unix()
	}
	completed := 0
	if p.Completed || (p.Duration > 60 && p.Position/p.Duration >= 0.90) {
		completed = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO watch_progress(
			media_key, media_type, tmdb_id, season, episode,
			title, episode_title, poster, backdrop,
			last_hash, last_file_idx, position, duration, completed, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(media_key) DO UPDATE SET
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE watch_progress.title END,
			episode_title = CASE WHEN excluded.episode_title != '' THEN excluded.episode_title ELSE watch_progress.episode_title END,
			poster = CASE WHEN excluded.poster != '' THEN excluded.poster ELSE watch_progress.poster END,
			backdrop = CASE WHEN excluded.backdrop != '' THEN excluded.backdrop ELSE watch_progress.backdrop END,
			last_hash = CASE WHEN excluded.last_hash != '' THEN excluded.last_hash ELSE watch_progress.last_hash END,
			last_file_idx = CASE WHEN excluded.last_file_idx >= 0 THEN excluded.last_file_idx ELSE watch_progress.last_file_idx END,
			position = excluded.position,
			duration = CASE WHEN excluded.duration > 0 THEN excluded.duration ELSE watch_progress.duration END,
			completed = MAX(watch_progress.completed, excluded.completed),
			updated_at = excluded.updated_at`,
		p.MediaKey, p.MediaType, p.TMDBID, p.Season, p.Episode,
		p.Title, p.EpisodeTitle, p.Poster, p.Backdrop,
		p.LastHash, p.LastFileIdx, p.Position, p.Duration, completed, p.UpdatedAt)
	return err
}

func scanProgress(row interface{ Scan(...any) error }) (WatchProgress, error) {
	var p WatchProgress
	var completed int
	err := row.Scan(
		&p.MediaKey, &p.MediaType, &p.TMDBID, &p.Season, &p.Episode,
		&p.Title, &p.EpisodeTitle, &p.Poster, &p.Backdrop,
		&p.LastHash, &p.LastFileIdx, &p.Position, &p.Duration, &completed, &p.UpdatedAt,
	)
	p.Completed = completed != 0
	return p, err
}

func (s *Store) GetProgress(ctx context.Context, mediaKey string) (WatchProgress, bool, error) {
	p, err := scanProgress(s.db.QueryRowContext(ctx, `
		SELECT media_key, media_type, tmdb_id, season, episode,
		       title, episode_title, poster, backdrop,
		       last_hash, last_file_idx, position, duration, completed, updated_at
		FROM watch_progress WHERE media_key=?`, mediaKey))
	if err == sql.ErrNoRows {
		return WatchProgress{}, false, nil
	}
	if err != nil {
		return WatchProgress{}, false, err
	}
	return p, true, nil
}

func (s *Store) ListSeriesProgress(ctx context.Context, tmdbID int) ([]WatchProgress, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT media_key, media_type, tmdb_id, season, episode,
		       title, episode_title, poster, backdrop,
		       last_hash, last_file_idx, position, duration, completed, updated_at
		FROM watch_progress
		WHERE media_type='episode' AND tmdb_id=?
		ORDER BY season ASC, episode ASC`, tmdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WatchProgress
	for rows.Next() {
		p, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListContinueWatching returns the most recently watched in-progress movies and
// latest episode per series (deduplicated per series so one binge doesn't fill the shelf).
func (s *Store) ListContinueWatching(ctx context.Context, limit int) ([]WatchProgress, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT media_key, media_type, tmdb_id, season, episode,
		       title, episode_title, poster, backdrop,
		       last_hash, last_file_idx, position, duration, completed, updated_at
		FROM watch_progress
		WHERE position > 5 OR completed = 1
		ORDER BY updated_at DESC
		LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WatchProgress
	seenSeries := map[int]bool{}
	for rows.Next() {
		p, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		if p.MediaType == "movie" && p.Completed {
			continue
		}
		if p.MediaType == "episode" {
			if seenSeries[p.TMDBID] {
				continue
			}
			seenSeries[p.TMDBID] = true
		}
		out = append(out, p)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}
