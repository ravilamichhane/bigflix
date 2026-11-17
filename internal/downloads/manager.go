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
)

type Record struct {
	Hash      string    `json:"hash"`
	Title     string    `json:"title"`
	Poster    string    `json:"poster"`
	MovieID   int       `json:"movie_id"`
	FileIndex int       `json:"file_index"`
	Keep      bool      `json:"keep"`
	Paused    bool      `json:"paused"`
	Info      []byte    `json:"info,omitempty"`
	Created   time.Time `json:"created"`
	Position  float64   `json:"position"`
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
}
type Options struct {
	MediaDir     string
	MaxBytes     int64
	ReserveBytes uint64
	ListenPort   int
	ClientConfig *torrent.ClientConfig
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
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		fs.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{subtitleService: subtitles.New(db), client: cl, store: db, storage: fs, dir: dir, jobs: map[string]*job{}, ctx: ctx, cancel: cancel, maxBytes: opts.MaxBytes, reserve: opts.ReserveBytes}
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
		if _, err = m.addLocked(r, nil); err != nil {
			m.Close()
			return nil, fmt.Errorf("restore download: %w", err)
		}
	}
	m.wg.Add(1)
	go m.loop()
	return m, nil
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
	if i.TotalLength() <= 0 || i.TotalLength() > 100_000_000_000 || i.NumPieces() > 1_000_000 {
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
	if j := m.jobs[r.Hash]; j != nil {
		if r.Keep && !j.record.Keep {
			j.record.Keep = true
			if err := m.save(j); err != nil {
				return nil, err
			}
		}
		return j, nil
	}
	if len(m.jobs) >= 20 {
		return nil, errors.New("local prototype supports at most 20 downloads")
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
	j := &job{record: r, t: t, priorities: map[int]torrent.PiecePriority{}, readers: map[uint64]context.CancelFunc{}, last: time.Now()}
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
	j, err := m.addLocked(Record{Hash: hash, Title: title, Poster: poster, MovieID: movieID, Keep: keep, FileIndex: -1, Created: time.Now()}, nil)
	if err != nil {
		return Status{}, err
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
	j, err := m.addLocked(Record{Hash: mi.HashInfoBytes().HexString(), Title: title, FileIndex: -1, Info: mi.InfoBytes, Created: time.Now()}, spec)
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
		case ".mp4", ".m4v", ".webm", ".mkv", ".mov":
			if f.Length() > 0 {
				out = append(out, VideoFile{idx, f.DisplayPath(), f.Length(), directExt(ext) || (FFmpeg != "" && ext != ".webm")})
			}
		}
	}
	return out
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
		s.Progress = float64(s.Completed) / float64(s.Size)
		s.State = "downloading"
		if s.Completed >= s.Size {
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
	if j.record.Paused {
		s.State = "paused"
	}
	if j.err != "" && !j.record.Paused {
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
		out = append(out, m.status(j))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
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

func (m *Manager) Update(hash, action string, index int, position float64) error {
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
		if len(j.readers) > 0 {
			return errors.New("stop playback before changing files")
		}
		found := false
		for _, f := range videos(j.t) {
			if f.Index == index {
				found = true
			}
		}
		if !found {
			return errors.New("invalid video file")
		}
		j.record.FileIndex = index
		j.record.Position = 0
	case "progress":
		if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 || position > 1e7 {
			return errors.New("invalid position")
		}
		j.record.Position = position
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
		j.t.AllowDataDownload()
	case "select":
		for p := range j.priorities {
			j.t.Piece(p).SetPriority(torrent.PiecePriorityNone)
		}
		j.priorities = map[int]torrent.PiecePriority{}
		j.file = j.t.Files()[index]
		j.record.Position = 0
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
			for _, j := range m.jobs {
				m.schedule(j)
				m.recoverPeersLocked(j, time.Now())
			}
			m.mu.Unlock()
		}
	}
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
		var total int64
		for _, other := range m.jobs {
			if other.file != nil {
				total += other.file.Length()
			}
		}
		if total > m.maxBytes {
			j.err = "Configured download budget exceeded; increase MEDIA_BUDGET_GB and restart"
			j.t.DisallowDataDownload()
			return
		}
		var disk unix.Statfs_t
		if err := unix.Statfs(m.dir, &disk); err != nil {
			j.err = "Cannot check free disk space"
			j.t.DisallowDataDownload()
			return
		}
		if uint64(disk.Bavail)*uint64(disk.Bsize) < m.reserve {
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
		window := max(1, int(backgroundWindow/pl))
		for p := first; p < min(end, first+window); p++ {
			if !j.t.PieceState(p).Complete {
				next[p] = torrent.PiecePriorityNormal
			}
		} // Beginning and tail accelerate container probing; actual seek readers outrank these.
		edge := max(1, int((1<<20)/pl))
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
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[hash]
	if j == nil {
		return nil, VideoFile{}, nil, ErrNotFound
	}
	if j.record.Paused {
		return nil, VideoFile{}, nil, ErrPaused
	}
	if j.file == nil || j.err != "" {
		return nil, VideoFile{}, nil, ErrNotReady
	}
	if len(j.readers) >= 12 {
		return nil, VideoFile{}, nil, errors.New("too many playback requests")
	}
	ctx, cancel := context.WithCancel(ctx)
	reader := j.file.NewReader()
	reader.SetContext(ctx)
	reader.SetReadahead(readAhead)
	m.nextReader++
	id := m.nextReader
	j.readers[id] = cancel
	var once sync.Once
	release := func() {
		once.Do(func() { cancel(); reader.Close(); m.mu.Lock(); delete(j.readers, id); m.mu.Unlock() })
	}
	return reader, VideoFile{Index: j.record.FileIndex, Name: j.file.DisplayPath(), Size: j.file.Length()}, release, nil
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
