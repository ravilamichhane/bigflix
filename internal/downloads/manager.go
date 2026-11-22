package downloads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"bigflix/internal/database"
	"bigflix/internal/ranker"
	"bigflix/internal/subtitles"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/sys/unix"
)

var hashRE = regexp.MustCompile(`^[a-f0-9]{40}$`)
var ErrNotFound = errors.New("download not found")
var ErrNotReady = errors.New("video is not ready; wait for torrent metadata and select a file")
var ErrPaused = errors.New("download is paused; resume it before playback")

const (
	readAhead        int64 = 8 << 20
	backgroundWindow       = 8 << 20
	maxStoredJobs          = 200
)

// FileMapperFunc optionally maps ambiguous video file paths inside a multi-file
// torrent (e.g. anime season packs) to (season, episode) pairs via local AI.
type FileMapperFunc func(ctx context.Context, seriesTitle string, targetSeason int, files []VideoFile) ([]EpisodeFile, error)

// streamWindow scales piece readahead and header/tail edge priority with file
// size so high-bitrate 4K/1080p streams keep enough verified data queued ahead.
func streamWindow(size int64) (ahead, bg, edge int64) {
	switch {
	case size >= 2<<30:
		return 24 << 20, 24 << 20, 4 << 20
	case size >= 500<<20:
		return 16 << 20, 16 << 20, 2 << 20
	default:
		return readAhead, backgroundWindow, 1 << 20
	}
}

type EpisodeFile struct {
	FileIndex int    `json:"file_index"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
}

type FallbackCandidate struct {
	Hash         string `json:"hash"`
	Label        string `json:"label"`
	IsSeasonPack bool   `json:"is_season_pack,omitempty"`
}

type Record struct {
	Hash              string              `json:"hash"`
	Title             string              `json:"title"`
	Poster            string              `json:"poster"`
	Backdrop          string              `json:"backdrop,omitempty"`
	MovieID           int                 `json:"movie_id,omitempty"`
	MediaType         string              `json:"media_type,omitempty"` // "movie" | "episode"
	TMDBID            int                 `json:"tmdb_id,omitempty"`
	IMDbID            string              `json:"imdb_id,omitempty"`
	Season            int                 `json:"season,omitempty"`
	Episode           int                 `json:"episode,omitempty"`
	EpisodeTitle      string              `json:"episode_title,omitempty"`
	FileIndex         int                 `json:"file_index"`
	EpisodeFiles      []EpisodeFile       `json:"episode_files,omitempty"`
	ActiveSourceLabel string              `json:"active_source_label,omitempty"`
	Fallbacks         []FallbackCandidate `json:"fallbacks,omitempty"`
	FailoverAttempt   int                 `json:"failover_attempt,omitempty"`
	FailoverTotal     int                 `json:"failover_total,omitempty"`
	ReplacedByHash    string              `json:"replaced_by_hash,omitempty"`
	FailoverNote      string              `json:"failover_note,omitempty"`
	Keep              bool                `json:"keep"`
	Paused            bool                `json:"paused"`
	Info              []byte              `json:"info,omitempty"`
	Created           time.Time           `json:"created"`
	LastAccessed      time.Time           `json:"last_accessed,omitempty"`
	Position          float64             `json:"position"`
}

type MediaRequest struct {
	Hash              string
	Title             string
	Poster            string
	Backdrop          string
	MediaType         string // "movie" | "episode"
	TMDBID            int
	IMDbID            string
	Season            int
	Episode           int
	EpisodeTitle      string
	ActiveSourceLabel string
	Keep              bool
	Fallbacks         []FallbackCandidate
}

type VideoFile struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Playable bool   `json:"playable"`
}

type Status struct {
	Record
	Info        []byte      `json:"-"`
	State       string      `json:"state"`
	Error       string      `json:"error,omitempty"`
	Files       []VideoFile `json:"files"`
	Size        int64       `json:"size"`
	Completed   int64       `json:"completed"`
	Buffered    int64       `json:"buffered"`
	Speed       int64       `json:"speed"`
	Peers       int         `json:"peers"`
	Discovering bool        `json:"discovering"`
	Readers     int         `json:"readers"`
	Progress    float64     `json:"progress"`
	Playable    bool        `json:"playable"`
	Direct      bool        `json:"direct"`
}

type job struct {
	discovering   bool
	lastDiscovery time.Time
	peerlessSince time.Time
	startedAt     time.Time
	replacedAt    time.Time
	aiMapped      bool
	record        Record
	t             *torrent.Torrent
	file          *torrent.File
	err           string
	priorities    map[int]torrent.PiecePriority
	readers       map[uint64]context.CancelFunc
	prev          int64
	speed         int64
	last          time.Time
}

type Manager struct {
	subtitleService *subtitles.Service
	mu              sync.Mutex
	client          *torrent.Client
	store           *database.Store
	storage         storage.ClientImplCloser
	dir             string
	jobs            map[string]*job
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	nextReader      uint64
	maxBytes        int64
	reserve         uint64
	closed          bool
	listenPort      int
	bindings        []InterfaceBinding
	networkClosers  []func()
	networkErrors   []string
	failoverTimeout time.Duration
	fileMapper      FileMapperFunc
}

type Options struct {
	MediaDir        string
	MaxBytes        int64
	ReserveBytes    uint64
	ListenPort      int
	ClientConfig    *torrent.ClientConfig
	FailoverTimeout time.Duration
}

func Open(db *database.Store, opts Options) (*Manager, error) {
	dir, err := filepath.Abs(opts.MediaDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err = db.InitDownloads(context.Background()); err != nil {
		return nil, err
	}
	fs := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: dir, PieceCompletion: storage.NewMapPieceCompletion(), TorrentDirMaker: func(base string, _ *metainfo.Info, h metainfo.Hash) string { return filepath.Join(base, h.HexString()) }, FilePathMaker: func(o storage.FilePathMakerOpts) string {
		sum := sha256.Sum256([]byte(o.Info.BestName() + "/" + strings.Join(o.File.BestPath(), "/")))
		return hex.EncodeToString(sum[:]) + ".media"
	}})
	cfg := opts.ClientConfig
	var bindings []InterfaceBinding
	if cfg == nil {
		b, e := db.NetworkConfig(context.Background())
		if e != nil {
			fs.Close()
			return nil, e
		}
		if len(b) > 0 {
			if e = json.Unmarshal(b, &bindings); e != nil {
				fs.Close()
				return nil, e
			}
		}
		cfg = torrent.NewDefaultClientConfig()
		cfg.ListenPort, err = db.TorrentPort(context.Background(), opts.ListenPort)
		if err != nil {
			fs.Close()
			return nil, err
		}
		if cfg.ListenPort < 0 || cfg.ListenPort > 65535 {
			fs.Close()
			return nil, errors.New("invalid torrent port")
		}
		cfg.ListenHost = func(string) string { return "" }
		cfg.DisableWebtorrent = true
		cfg.DisableWebseeds = true
		cfg.Seed = false
		cfg.EstablishedConnsPerTorrent = 60
		cfg.HalfOpenConnsPerTorrent = 30
		if len(bindings) > 0 {
			cfg.DisableTCP = true
			cfg.DisableUTP = true
			cfg.NoDHT = true
			cfg.NoDefaultPortForwarding = true
		}
	}
	cfg.DataDir = dir
	cfg.DefaultStorage = &guardedStorage{base: fs, dir: dir}
	if cfg.Slogger == nil {
		cfg.Slogger = slog.New(torrentLogHandler{base: slog.Default().Handler()})
	}
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		fs.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	ft := opts.FailoverTimeout
	if ft <= 0 {
		ft = 18 * time.Second
	}
	m := &Manager{
		subtitleService: subtitles.New(db),
		client:          cl,
		store:           db,
		storage:         fs,
		dir:             dir,
		jobs:            map[string]*job{},
		ctx:             ctx,
		cancel:          cancel,
		maxBytes:        opts.MaxBytes,
		reserve:         opts.ReserveBytes,
		failoverTimeout: ft,
	}
	m.listenPort = cfg.ListenPort
	m.bindings = bindings
	if len(bindings) > 0 {
		m.openInterfaceSockets()
	}
	if m.maxBytes <= 0 {
		m.maxBytes = 50_000_000_000
	}
	records, err := db.Downloads(ctx)
	if err != nil {
		m.Close()
		return nil, err
	}
	for _, b := range records {
		var r Record
		if err = json.Unmarshal(b, &r); err != nil {
			m.Close()
			return nil, err
		}
		if !hashRE.MatchString(r.Hash) {
			m.Close()
			return nil, errors.New("invalid saved torrent hash")
		}
		// Don't keep dead replaced torrents across restarts
		if r.ReplacedByHash != "" {
			_ = db.DeleteDownload(ctx, r.Hash)
			_ = os.RemoveAll(filepath.Join(dir, r.Hash))
			continue
		}
		if r.LastAccessed.IsZero() {
			r.LastAccessed = r.Created
		}
		if _, err = m.addLocked(r, nil); err != nil {
			m.Close()
			return nil, fmt.Errorf("restore download: %w", err)
		}
	}
	m.wg.Add(1)
	go m.loop()
	return m, nil
}

func (m *Manager) SetFileMapper(fn FileMapperFunc) {
	m.mu.Lock()
	m.fileMapper = fn
	m.mu.Unlock()
}

// torrentLogHandler silences noisy anacrolix/torrent reader errors caused by
// normal browser/FFmpeg HTTP Range closes (err="context canceled") while still
// forwarding genuine warnings and errors.
type torrentLogHandler struct {
	base slog.Handler
}

func (h torrentLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn && h.base.Enabled(ctx, level)
}

func (h torrentLogHandler) Handle(ctx context.Context, r slog.Record) error {
	if strings.Contains(r.Message, "read failed") {
		canceled := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "err" && strings.Contains(a.Value.String(), "context canceled") {
				canceled = true
				return false
			}
			return true
		})
		if canceled {
			return nil
		}
	}
	return h.base.Handle(ctx, r)
}

func (h torrentLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return torrentLogHandler{base: h.base.WithAttrs(attrs)}
}

func (h torrentLogHandler) WithGroup(name string) slog.Handler {
	return torrentLogHandler{base: h.base.WithGroup(name)}
}

// Use opaque filenames and reject symlinks in the storage unit before opening it.
type guardedStorage struct {
	base storage.ClientImpl
	dir  string
}

func (g *guardedStorage) OpenTorrent(ctx context.Context, i *metainfo.Info, h metainfo.Hash) (storage.TorrentImpl, error) {
	dir := filepath.Join(g.dir, h.HexString())
	for _, p := range []string{g.dir, dir} {
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return storage.TorrentImpl{}, errors.New("symlink storage rejected")
		}
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				return storage.TorrentImpl{}, errors.New("symlink media rejected")
			}
		}
	}
	if i.TotalLength() <= 0 || i.TotalLength() > 2_000_000_000_000 || i.NumPieces() > 2_000_000 {
		return storage.TorrentImpl{}, errors.New("torrent exceeds storage metadata limits")
	}
	return g.base.OpenTorrent(ctx, i, h)
}

func (m *Manager) save(j *job) error {
	b, err := json.Marshal(j.record)
	if err != nil {
		return err
	}
	return m.store.SaveDownload(m.ctx, j.record.Hash, b)
}

func (m *Manager) addLocked(r Record, spec *torrent.TorrentSpec) (*job, error) {
	if m.closed {
		return nil, errors.New("download service is stopping")
	}
	now := time.Now()
	if r.LastAccessed.IsZero() {
		r.LastAccessed = now
	}
	if j := m.jobs[r.Hash]; j != nil {
		changed := false
		j.record.LastAccessed = now
		if r.Keep && !j.record.Keep {
			j.record.Keep = true
			changed = true
		}
		if j.record.ReplacedByHash != "" {
			j.record.ReplacedByHash = ""
			j.replacedAt = time.Time{}
			j.record.Paused = false
			j.err = ""
			j.startedAt = now
			j.t.AllowDataDownload()
			changed = true
		}
		if r.MediaType != "" && j.record.MediaType == "" {
			j.record.MediaType = r.MediaType
			j.record.TMDBID = r.TMDBID
			j.record.IMDbID = r.IMDbID
			changed = true
		}
		if r.MediaType == "episode" && (r.Season != j.record.Season || r.Episode != j.record.Episode) {
			j.record.Season = r.Season
			j.record.Episode = r.Episode
			j.record.EpisodeTitle = r.EpisodeTitle
			j.record.Paused = false
			j.err = ""
			j.t.AllowDataDownload()
			if idx, ok := findEpisodeFileIndex(j.record.EpisodeFiles, r.Season, r.Episode); ok && j.t.Info() != nil && idx < len(j.t.Files()) {
				m.switchJobFileLocked(j, idx)
			} else {
				// Re-evaluate file selection for new target episode
				m.clearPrioritiesLocked(j)
				j.file = nil
				j.record.FileIndex = -1
			}
			changed = true
		}
		if changed {
			if err := m.save(j); err != nil {
				return nil, err
			}
		}
		return j, nil
	}
	if len(m.jobs) >= maxStoredJobs {
		if !m.evictOneOldestLocked("") {
			return nil, errors.New("maximum stored downloads limit reached; delete or unkeep old items")
		}
	}
	if spec == nil {
		var err error
		spec, err = torrent.TorrentSpecFromMagnetUri("magnet:?xt=urn:btih:" + r.Hash)
		if err != nil {
			return nil, err
		}
		spec.InfoBytes = r.Info
	}
	spec.DisallowDataDownload = r.Paused
	spec.IgnoreUnverifiedPieceCompletion = true
	spec.Sources = nil
	spec.Webseeds = nil
	spec.Trackers = nil
	t, _, err := m.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, err
	}
	j := &job{
		record:     r,
		t:          t,
		priorities: map[int]torrent.PiecePriority{},
		readers:    map[uint64]context.CancelFunc{},
		last:       now,
		startedAt:  now,
	}
	if err = m.save(j); err != nil {
		t.Drop()
		return nil, err
	}
	m.jobs[r.Hash] = j
	return j, nil
}

func (m *Manager) Add(hash, title, poster string, movieID int, keep bool) (Status, error) {
	hash = strings.ToLower(hash)
	if !hashRE.MatchString(hash) {
		return Status{}, errors.New("invalid torrent hash")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	j, err := m.addLocked(Record{
		Hash:         hash,
		Title:        title,
		Poster:       poster,
		MovieID:      movieID,
		MediaType:    "movie",
		Keep:         keep,
		FileIndex:    -1,
		Created:      now,
		LastAccessed: now,
	}, nil)
	if err != nil {
		return Status{}, err
	}
	return m.status(j), nil
}

func (m *Manager) AddMedia(req MediaRequest) (Status, error) {
	hash := strings.ToLower(strings.TrimSpace(req.Hash))
	if !hashRE.MatchString(hash) {
		return Status{}, errors.New("invalid torrent hash")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	total := 1 + len(req.Fallbacks)
	var pos float64
	if req.TMDBID > 0 && m.store != nil {
		if p, ok, err := m.store.GetProgress(m.ctx, database.MediaKey(req.MediaType, req.TMDBID, req.Season, req.Episode)); err == nil && ok && !p.Completed {
			pos = p.Position
		}
	}
	r := Record{
		Hash:              hash,
		Title:             req.Title,
		Poster:            req.Poster,
		Backdrop:          req.Backdrop,
		MediaType:         req.MediaType,
		TMDBID:            req.TMDBID,
		IMDbID:            req.IMDbID,
		Season:            req.Season,
		Episode:           req.Episode,
		EpisodeTitle:      req.EpisodeTitle,
		ActiveSourceLabel: req.ActiveSourceLabel,
		Fallbacks:         req.Fallbacks,
		FailoverAttempt:   1,
		FailoverTotal:     total,
		Keep:              req.Keep,
		FileIndex:         -1,
		Created:           now,
		LastAccessed:      now,
		Position:          pos,
	}
	j, err := m.addLocked(r, nil)
	if err != nil {
		return Status{}, err
	}
	if pos > 0 && j.record.Position == 0 {
		j.record.Position = pos
		_ = m.save(j)
	}
	return m.status(j), nil
}

// AddMeta is also used by the isolated local-seeder validation; it does not fetch any remote URLs.
func (m *Manager) AddMeta(mi *metainfo.MetaInfo, title string) (Status, error) {
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	j, err := m.addLocked(Record{Hash: mi.HashInfoBytes().HexString(), Title: title, FileIndex: -1, Info: mi.InfoBytes, Created: now, LastAccessed: now}, spec)
	if err != nil {
		return Status{}, err
	}
	return m.status(j), nil
}

// AddMetaMedia supports local-seeder testing of TV episodes and multi-file season packs.
func (m *Manager) AddMetaMedia(mi *metainfo.MetaInfo, req MediaRequest) (Status, error) {
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	j, err := m.addLocked(Record{
		Hash:              mi.HashInfoBytes().HexString(),
		Title:             req.Title,
		Poster:            req.Poster,
		MediaType:         req.MediaType,
		TMDBID:            req.TMDBID,
		IMDbID:            req.IMDbID,
		Season:            req.Season,
		Episode:           req.Episode,
		EpisodeTitle:      req.EpisodeTitle,
		ActiveSourceLabel: req.ActiveSourceLabel,
		Fallbacks:         req.Fallbacks,
		FailoverAttempt:   1,
		FailoverTotal:     1 + len(req.Fallbacks),
		Keep:              req.Keep,
		FileIndex:         -1,
		Info:              mi.InfoBytes,
		Created:           now,
		LastAccessed:      now,
	}, spec)
	if err != nil {
		return Status{}, err
	}
	return m.status(j), nil
}

func videos(t *torrent.Torrent) []VideoFile {
	var out []VideoFile
	if t.Info() == nil {
		return out
	}
	for idx, f := range t.Files() {
		ext := strings.ToLower(filepath.Ext(f.Path()))
		switch ext {
		case ".mp4", ".m4v", ".webm", ".mkv", ".mov", ".ts", ".m2ts", ".avi":
			if f.Length() > 0 {
				out = append(out, VideoFile{idx, f.DisplayPath(), f.Length(), directExt(ext) || (FFmpeg != "" && ext != ".webm")})
			}
		}
	}
	return out
}

func mapEpisodeFilesRegex(files []VideoFile, fallbackSeason int) []EpisodeFile {
	var out []EpisodeFile
	for _, f := range files {
		s, e, ok := ranker.ParseEpisodeFilename(f.Name)
		if !ok || e <= 0 {
			continue
		}
		if s == 0 {
			s = fallbackSeason
		}
		out = append(out, EpisodeFile{
			FileIndex: f.Index,
			Season:    s,
			Episode:   e,
			Name:      f.Name,
			Size:      f.Size,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Episode < out[j].Episode
	})
	return out
}

func findEpisodeFileIndex(mapped []EpisodeFile, season, episode int) (int, bool) {
	for _, ef := range mapped {
		if ef.Season == season && ef.Episode == episode {
			return ef.FileIndex, true
		}
	}
	// Fallback for single-season anime packs where season wasn't in filenames
	for _, ef := range mapped {
		if ef.Season == 0 && ef.Episode == episode {
			return ef.FileIndex, true
		}
	}
	return -1, false
}

func (m *Manager) status(j *job) Status {
	s := Status{Record: j.record, State: "metadata", Error: j.err, Speed: j.speed, Readers: len(j.readers)}
	s.Discovering = j.discovering
	s.Record.Info = nil
	s.Peers = len(j.t.PeerConns())
	if j.t.Info() != nil {
		s.Files = videos(j.t)
		s.State = "select-file"
	}
	if j.file != nil {
		s.Size = j.file.Length()
		s.Completed = j.file.BytesCompleted()
		if s.Size > 0 {
			s.Progress = float64(s.Completed) / float64(s.Size)
		}
		s.State = "downloading"
		if s.Completed >= s.Size && s.Size > 0 {
			s.State = "complete"
		}
		ext := strings.ToLower(filepath.Ext(j.file.Path()))
		s.Direct = directExt(ext)
		s.Playable = s.Direct || FFmpeg != ""
		pieceLen := j.t.Info().PieceLength
		offset := j.file.Offset()
		end := offset + s.Size
		for p := int(offset / pieceLen); p < int((end+pieceLen-1)/pieceLen); p++ {
			if !j.t.PieceState(p).Complete {
				break
			}
			s.Buffered += min(end, int64(p+1)*pieceLen) - max(offset, int64(p)*pieceLen)
		}
	}
	if j.record.ReplacedByHash != "" {
		s.State = "failover"
	} else if j.record.Paused {
		s.State = "paused"
	}
	if j.err != "" && !j.record.Paused && j.record.ReplacedByHash == "" {
		s.State = "error"
	}
	return s
}

func (m *Manager) Get(hash string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[hash]
	if j == nil {
		return Status{}, ErrNotFound
	}
	return m.status(j), nil
}

func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.record.ReplacedByHash != "" {
			continue
		}
		out = append(out, m.status(j))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (m *Manager) StorageUsage() (used int64, budget int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.record.ReplacedByHash != "" {
			continue
		}
		if j.t.Info() != nil {
			used += j.t.BytesCompleted()
		}
	}
	return used, m.maxBytes
}

// FindExistingMedia returns an existing download hash (and file index) for a
// movie or TV episode, including episodes mapped inside a season-pack torrent.
func (m *Manager) FindExistingMedia(mediaType string, tmdbID, season, episode int) (hash string, fileIndex int, ok bool) {
	if tmdbID <= 0 {
		return "", -1, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.record.ReplacedByHash != "" || j.err != "" {
			continue
		}
		if j.record.TMDBID != tmdbID || j.record.MediaType != mediaType {
			continue
		}
		if mediaType == "movie" {
			return j.record.Hash, j.record.FileIndex, true
		}
		if j.record.Season == season && j.record.Episode == episode {
			return j.record.Hash, j.record.FileIndex, true
		}
		if idx, found := findEpisodeFileIndex(j.record.EpisodeFiles, season, episode); found {
			return j.record.Hash, idx, true
		}
	}
	return "", -1, false
}

// FindSeriesDownloads returns a map of "S:E" -> (hash, fileIndex) for all active
// downloads matching a TV series (both standalone episodes and season packs).
func (m *Manager) FindSeriesDownloads(tmdbID int) map[[2]int]struct {
	Hash      string
	FileIndex int
} {
	out := map[[2]int]struct {
		Hash      string
		FileIndex int
	}{}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.record.ReplacedByHash != "" || j.record.MediaType != "episode" || j.record.TMDBID != tmdbID {
			continue
		}
		if len(j.record.EpisodeFiles) > 0 {
			for _, ef := range j.record.EpisodeFiles {
				out[[2]int{ef.Season, ef.Episode}] = struct {
					Hash      string
					FileIndex int
				}{j.record.Hash, ef.FileIndex}
			}
		} else if j.record.Season > 0 && j.record.Episode > 0 {
			out[[2]int{j.record.Season, j.record.Episode}] = struct {
				Hash      string
				FileIndex int
			}{j.record.Hash, j.record.FileIndex}
		}
	}
	return out
}

func (m *Manager) Store() *database.Store { return m.store }

func (m *Manager) Delete(hash string) error {
	hash = strings.ToLower(hash)
	if !hashRE.MatchString(hash) {
		return errors.New("invalid torrent hash")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteLocked(hash)
}

func (m *Manager) deleteLocked(hash string) error {
	if m.closed {
		return errors.New("download service is stopping")
	}
	j := m.jobs[hash]
	if j == nil {
		return ErrNotFound
	}
	for id, cancel := range j.readers {
		cancel()
		delete(j.readers, id)
	}
	j.t.Drop()
	delete(m.jobs, hash)
	if err := m.store.DeleteDownload(m.ctx, hash); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(m.dir, hash)); err != nil {
		return err
	}
	return nil
}

func (m *Manager) clearPrioritiesLocked(j *job) {
	for p := range j.priorities {
		j.t.Piece(p).SetPriority(torrent.PiecePriorityNone)
	}
	j.priorities = map[int]torrent.PiecePriority{}
}

func (m *Manager) switchJobFileLocked(j *job, index int) {
	for id, cancel := range j.readers {
		cancel()
		delete(j.readers, id)
	}
	m.clearPrioritiesLocked(j)
	j.record.FileIndex = index
	j.file = j.t.Files()[index]
	j.prev = j.file.BytesCompleted()
	j.speed = 0
	j.record.LastAccessed = time.Now()
	// Load decoupled progress for this episode if available
	if j.record.TMDBID > 0 && m.store != nil {
		if p, ok, err := m.store.GetProgress(m.ctx, database.MediaKey(j.record.MediaType, j.record.TMDBID, j.record.Season, j.record.Episode)); err == nil && ok && !p.Completed {
			j.record.Position = p.Position
		} else {
			j.record.Position = 0
		}
	} else {
		j.record.Position = 0
	}
}

func (m *Manager) Update(hash, action string, index int, position float64) error {
	return m.UpdateWithDuration(hash, action, index, position, 0)
}

func (m *Manager) UpdateWithDuration(hash, action string, index int, position, duration float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if action == "delete" {
		return m.deleteLocked(strings.ToLower(hash))
	}
	j := m.jobs[hash]
	if j == nil {
		return ErrNotFound
	}
	if m.closed {
		return errors.New("download service is stopping")
	}
	if action == "reconnect" {
		if j.record.Paused {
			return ErrPaused
		}
		m.discoverLocked(j, time.Now())
		return nil
	}
	old := j.record
	j.record.LastAccessed = time.Now()
	switch action {
	case "pause":
		j.record.Paused = true
	case "resume":
		j.record.Paused = false
	case "keep":
		j.record.Keep = true
	case "unkeep":
		j.record.Keep = false
	case "select":
		found := false
		for _, f := range videos(j.t) {
			if f.Index == index {
				found = true
			}
		}
		if !found {
			return errors.New("invalid video file")
		}
		if len(j.readers) > 0 && j.record.MediaType != "episode" {
			return errors.New("stop playback before changing files")
		}
		// Update season/episode metadata if selecting a mapped episode in a season pack
		for _, ef := range j.record.EpisodeFiles {
			if ef.FileIndex == index {
				j.record.Season = ef.Season
				j.record.Episode = ef.Episode
				break
			}
		}
		m.switchJobFileLocked(j, index)
	case "progress":
		if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 || position > 1e7 {
			return errors.New("invalid position")
		}
		j.record.Position = position
		if j.record.TMDBID > 0 && m.store != nil {
			mt := j.record.MediaType
			if mt == "" {
				mt = "movie"
			}
			_ = m.store.UpsertProgress(m.ctx, database.WatchProgress{
				MediaType:    mt,
				TMDBID:       j.record.TMDBID,
				Season:       j.record.Season,
				Episode:      j.record.Episode,
				Title:        j.record.Title,
				EpisodeTitle: j.record.EpisodeTitle,
				Poster:       j.record.Poster,
				Backdrop:     j.record.Backdrop,
				LastHash:     j.record.Hash,
				LastFileIdx:  j.record.FileIndex,
				Position:     position,
				Duration:     duration,
			})
		}
	default:
		return errors.New("unknown action")
	}
	if err := m.save(j); err != nil {
		j.record = old
		return err
	}
	switch action {
	case "pause":
		j.t.DisallowDataDownload()
		for _, cancel := range j.readers {
			cancel()
		}
	case "resume":
		j.err = ""
		j.startedAt = time.Now()
		j.t.AllowDataDownload()
	}
	return nil
}

func (m *Manager) loop() {
	defer m.wg.Done()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
			m.mu.Lock()
			now := time.Now()
			// Snapshot job pointers so failover/eviction mutations are safe
			list := make([]*job, 0, len(m.jobs))
			for _, j := range m.jobs {
				list = append(list, j)
			}
			for _, j := range list {
				if m.jobs[j.record.Hash] != j {
					continue
				}
				// Clean up dead replaced jobs after grace period so WatchPage can follow ReplacedByHash
				if j.record.ReplacedByHash != "" {
					if !j.replacedAt.IsZero() && now.Sub(j.replacedAt) > 60*time.Second && len(j.readers) == 0 {
						_ = m.deleteLocked(j.record.Hash)
					}
					continue
				}
				m.schedule(j)
				if m.jobs[j.record.Hash] != j {
					continue
				}
				m.maybeFailoverLocked(j, now)
				if j.record.ReplacedByHash == "" {
					m.recoverPeersLocked(j, now)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) maybeFailoverLocked(j *job, now time.Time) {
	if j.record.Paused || j.record.ReplacedByHash != "" || len(j.record.Fallbacks) == 0 {
		return
	}
	shouldFailover := false
	reason := ""
	switch {
	case j.err != "" && !strings.Contains(j.err, "budget") && !strings.Contains(j.err, "Free-space"):
		shouldFailover = true
		reason = j.err
	case j.t.Info() == nil && len(j.t.PeerConns()) == 0 && now.Sub(j.startedAt) >= m.failoverTimeout:
		shouldFailover = true
		reason = "no peers for metadata"
	case j.file != nil && j.file.BytesCompleted() == 0 && len(j.t.PeerConns()) == 0 && now.Sub(j.startedAt) >= m.failoverTimeout:
		shouldFailover = true
		reason = "source stalled with 0 peers"
	}
	if !shouldFailover {
		return
	}

	next := j.record.Fallbacks[0]
	rest := append([]FallbackCandidate(nil), j.record.Fallbacks[1:]...)
	nextHash := strings.ToLower(strings.TrimSpace(next.Hash))
	if !hashRE.MatchString(nextHash) || nextHash == j.record.Hash {
		j.record.Fallbacks = rest
		_ = m.save(j)
		return
	}

	attempt := j.record.FailoverAttempt + 1
	total := j.record.FailoverTotal
	if total < attempt {
		total = attempt + len(rest)
	}
	note := fmt.Sprintf("Switched to backup source (%d/%d) — previous %s", attempt, total, reason)
	slog.Info("auto-failover switching torrent source", "from", j.record.Hash, "to", nextHash, "attempt", attempt, "total", total, "reason", reason)

	replacement := Record{
		Hash:              nextHash,
		Title:             j.record.Title,
		Poster:            j.record.Poster,
		Backdrop:          j.record.Backdrop,
		MediaType:         j.record.MediaType,
		TMDBID:            j.record.TMDBID,
		IMDbID:            j.record.IMDbID,
		Season:            j.record.Season,
		Episode:           j.record.Episode,
		EpisodeTitle:      j.record.EpisodeTitle,
		ActiveSourceLabel: next.Label,
		Fallbacks:         rest,
		FailoverAttempt:   attempt,
		FailoverTotal:     total,
		FailoverNote:      note,
		Keep:              j.record.Keep,
		FileIndex:         -1,
		Created:           now,
		LastAccessed:      now,
		Position:          j.record.Position,
	}
	if _, err := m.addLocked(replacement, nil); err != nil {
		slog.Warn("auto-failover add failed", "to", nextHash, "error", err)
		j.record.Fallbacks = rest
		_ = m.save(j)
		return
	}

	j.record.ReplacedByHash = nextHash
	j.record.FailoverNote = note
	j.record.Paused = true
	j.replacedAt = now
	j.t.DisallowDataDownload()
	for id, cancel := range j.readers {
		cancel()
		delete(j.readers, id)
	}
	_ = m.save(j)
}

func (m *Manager) autoEvictEnabled() bool {
	if m.store == nil {
		return true
	}
	return m.store.LoadPlaybackPrefs(m.ctx).AutoEvict
}

// evictOneOldestLocked removes the least-recently-accessed un-kept job with zero
// active playback readers. Returns true if a job was evicted.
func (m *Manager) evictOneOldestLocked(protectHash string) bool {
	var victim *job
	for _, other := range m.jobs {
		if other.record.Hash == protectHash || other.record.Keep || len(other.readers) > 0 {
			continue
		}
		if victim == nil {
			victim = other
			continue
		}
		at := other.record.LastAccessed
		if at.IsZero() {
			at = other.record.Created
		}
		vt := victim.record.LastAccessed
		if vt.IsZero() {
			vt = victim.record.Created
		}
		if at.Before(vt) {
			victim = other
		}
	}
	if victim == nil {
		return false
	}
	slog.Info("LRU evicting cached download to free space", "hash", victim.record.Hash, "title", victim.record.Title)
	return m.deleteLocked(victim.record.Hash) == nil
}

func (m *Manager) totalBudgetBytesLocked() int64 {
	var total int64
	for _, other := range m.jobs {
		if other.record.ReplacedByHash != "" {
			continue
		}
		// Charge only the active episode file (plus any already-completed bytes on disk),
		// never the full multi-hundred-GB size of an un-downloaded complete-series pack.
		var unit int64
		if other.file != nil {
			unit = other.file.Length()
		}
		if other.t.Info() != nil {
			if completed := other.t.BytesCompleted(); completed > unit {
				unit = completed
			}
		}
		total += unit
	}
	return total
}

func (m *Manager) schedule(j *job) {
	if j.t.Info() == nil {
		return
	}
	if len(j.record.Info) == 0 {
		mi := j.t.Metainfo()
		j.record.Info = mi.InfoBytes
		if err := m.save(j); err != nil {
			j.err = "Cannot save torrent metadata"
			j.t.DisallowDataDownload()
			return
		}
	}
	if j.file == nil {
		files := videos(j.t)
		if len(files) == 0 {
			j.err = "No supported video file was found in this torrent"
			j.t.DisallowDataDownload()
			return
		}
		if j.record.FileIndex >= 0 {
			for _, v := range files {
				if v.Index == j.record.FileIndex {
					j.file = j.t.Files()[v.Index]
				}
			}
		} else if j.record.MediaType == "episode" && len(files) > 1 {
			if len(j.record.EpisodeFiles) == 0 {
				j.record.EpisodeFiles = mapEpisodeFilesRegex(files, j.record.Season)
				_ = m.save(j)
			}
			if idx, ok := findEpisodeFileIndex(j.record.EpisodeFiles, j.record.Season, j.record.Episode); ok {
				j.record.FileIndex = idx
				j.file = j.t.Files()[idx]
				if err := m.save(j); err != nil {
					j.err = "Cannot save episode selection"
					j.t.DisallowDataDownload()
					return
				}
			} else if !j.aiMapped && m.fileMapper != nil {
				j.aiMapped = true
				hashCopy := j.record.Hash
				titleCopy := j.record.Title
				seasonCopy := j.record.Season
				epCopy := j.record.Episode
				filesCopy := append([]VideoFile(nil), files...)
				sort.Slice(filesCopy, func(a, b int) bool { return filesCopy[a].Name < filesCopy[b].Name })
				if len(filesCopy) > 60 {
					center := max(0, epCopy-1)
					startIdx := max(0, min(center-20, len(filesCopy)-60))
					filesCopy = filesCopy[startIdx:min(len(filesCopy), startIdx+60)]
				}
				mapper := m.fileMapper
				m.wg.Add(1)
				go func() {
					defer m.wg.Done()
					mapped, err := mapper(m.ctx, titleCopy, seasonCopy, filesCopy)
					m.mu.Lock()
					defer m.mu.Unlock()
					cur := m.jobs[hashCopy]
					if cur == nil || cur.t.Info() == nil {
						return
					}
					if err == nil && len(mapped) > 0 {
						cur.record.EpisodeFiles = mapped
						if idx, ok := findEpisodeFileIndex(mapped, seasonCopy, epCopy); ok && idx < len(cur.t.Files()) {
							cur.record.FileIndex = idx
							cur.file = cur.t.Files()[idx]
							_ = m.save(cur)
							return
						}
					}
					if len(cur.record.Fallbacks) > 0 && cur.file == nil {
						cur.err = fmt.Sprintf("S%02dE%02d not found in pack", seasonCopy, epCopy)
					}
				}()
				return
			} else if j.aiMapped || m.fileMapper == nil {
				if len(j.record.Fallbacks) > 0 {
					j.err = fmt.Sprintf("S%02dE%02d not found in pack", j.record.Season, j.record.Episode)
					return
				}
			}
		} else if len(files) > 0 {
			sort.Slice(files, func(a, b int) bool { return files[a].Size > files[b].Size })
			if len(files) == 1 || files[0].Size > 2*files[1].Size {
				j.record.FileIndex = files[0].Index
				j.file = j.t.Files()[files[0].Index]
				if err := m.save(j); err != nil {
					j.err = "Cannot save file selection"
					j.t.DisallowDataDownload()
					return
				}
			}
		}
	}
	if j.file == nil || j.record.Paused || j.err != "" {
		return
	}
	done := j.file.BytesCompleted()
	if done < j.file.Length() {
		total := m.totalBudgetBytesLocked()
		if total > m.maxBytes && m.autoEvictEnabled() {
			for total > m.maxBytes && m.evictOneOldestLocked(j.record.Hash) {
				total = m.totalBudgetBytesLocked()
			}
		}
		if total > m.maxBytes {
			j.err = "Configured download budget exceeded; delete kept items or increase MEDIA_BUDGET_GB"
			j.t.DisallowDataDownload()
			return
		}
		var disk unix.Statfs_t
		if err := unix.Statfs(m.dir, &disk); err != nil {
			j.err = "Cannot check free disk space"
			j.t.DisallowDataDownload()
			return
		}
		freeBytes := uint64(disk.Bavail) * uint64(disk.Bsize)
		if freeBytes < m.reserve && m.autoEvictEnabled() {
			for freeBytes < m.reserve && m.evictOneOldestLocked(j.record.Hash) {
				if err := unix.Statfs(m.dir, &disk); err != nil {
					break
				}
				freeBytes = uint64(disk.Bavail) * uint64(disk.Bsize)
			}
		}
		if freeBytes < m.reserve {
			j.err = "Free-space reserve reached. Free disk space, then resume"
			j.record.Paused = true
			j.t.DisallowDataDownload()
			for _, cancel := range j.readers {
				cancel()
			}
			m.save(j)
			return
		}
	}
	now := time.Now()
	if elapsed := now.Sub(j.last).Seconds(); elapsed >= 1 {
		j.speed = max(0, int64(float64(done-j.prev)/elapsed))
		j.prev = done
		j.last = now
	}
	pl := j.t.Info().PieceLength
	start := int(j.file.Offset() / pl)
	end := int((j.file.Offset() + j.file.Length() + pl - 1) / pl)
	next := map[int]torrent.PiecePriority{}
	first := -1
	for p := start; p < end; p++ {
		if !j.t.PieceState(p).Complete {
			first = p
			break
		}
	}
	if first >= 0 {
		// Keep enough work queued to use several fast peers while preserving
		// sequential playback. Reader priorities still outrank this window.
		_, bgWin, edgeBytes := streamWindow(j.file.Length())
		window := max(1, int(bgWin/pl))
		for p := first; p < min(end, first+window); p++ {
			if !j.t.PieceState(p).Complete {
				next[p] = torrent.PiecePriorityNormal
			}
		} // Beginning and tail accelerate container probing; actual seek readers outrank these.
		edge := max(1, int(edgeBytes/pl))
		for p := start; p < min(end, start+edge); p++ {
			if !j.t.PieceState(p).Complete {
				next[p] = torrent.PiecePriorityHigh
			}
		}
		for p := max(start, end-edge); p < end; p++ {
			if !j.t.PieceState(p).Complete {
				next[p] = torrent.PiecePriorityHigh
			}
		}
	}
	for p, old := range j.priorities {
		if next[p] != old {
			j.t.Piece(p).SetPriority(next[p])
		}
	}
	for p, prio := range next {
		if j.priorities[p] != prio {
			j.t.Piece(p).SetPriority(prio)
		}
	}
	j.priorities = next
}

func (m *Manager) Reader(ctx context.Context, hash string) (torrent.Reader, VideoFile, func(), error) {
	return m.ReaderForFile(ctx, hash, -1)
}

func (m *Manager) ReaderForFile(ctx context.Context, hash string, fileIndex int) (torrent.Reader, VideoFile, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[hash]
	if j == nil {
		return nil, VideoFile{}, nil, ErrNotFound
	}
	if j.record.Paused {
		return nil, VideoFile{}, nil, ErrPaused
	}
	targetFile := j.file
	targetIdx := j.record.FileIndex
	if fileIndex >= 0 && j.t.Info() != nil {
		files := j.t.Files()
		if fileIndex >= len(files) {
			return nil, VideoFile{}, nil, ErrNotReady
		}
		targetFile = files[fileIndex]
		targetIdx = fileIndex
	}
	if targetFile == nil || j.err != "" {
		return nil, VideoFile{}, nil, ErrNotReady
	}
	if len(j.readers) >= 12 {
		return nil, VideoFile{}, nil, errors.New("too many playback requests")
	}
	j.record.LastAccessed = time.Now()
	ctx, cancel := context.WithCancel(ctx)
	reader := targetFile.NewReader()
	reader.SetContext(ctx)
	ahead, _, _ := streamWindow(targetFile.Length())
	reader.SetReadahead(ahead)
	m.nextReader++
	id := m.nextReader
	j.readers[id] = cancel
	var once sync.Once
	release := func() {
		once.Do(func() { cancel(); reader.Close(); m.mu.Lock(); delete(j.readers, id); m.mu.Unlock() })
	}
	return reader, VideoFile{Index: targetIdx, Name: targetFile.DisplayPath(), Size: targetFile.Length()}, release, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	for _, j := range m.jobs {
		for _, cancel := range j.readers {
			cancel()
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
	if m.subtitleService != nil {
		m.subtitleService.Client.CloseIdleConnections()
	}
	for _, closeNetwork := range m.networkClosers {
		closeNetwork()
	}
	m.client.Close()
	if err := m.storage.Close(); err != nil {
		slog.Warn("close torrent storage", "error", err)
	}
}

// ConnectPeer supports controlled local-seeder validation; it is not exposed as an HTTP endpoint.
func (m *Manager) ConnectPeer(hash string, addr net.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[hash]
	if j == nil {
		return ErrNotFound
	}
	j.t.AddPeers([]torrent.PeerInfo{{Addr: addr, Trusted: true}})
	return nil
}

// FFmpeg is the ffmpeg binary used to remux containers browsers cannot open
// directly (for example MKV with HEVC). Empty when unavailable.
var FFmpeg, _ = exec.LookPath("ffmpeg")

func directExt(ext string) bool { return ext == ".mp4" || ext == ".m4v" || ext == ".webm" }
