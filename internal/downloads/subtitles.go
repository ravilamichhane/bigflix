package downloads

import (
	"bigflix/internal/subtitles"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

func (m *Manager) SubtitleService() *subtitles.Service { return m.subtitleService }

type SubtitleFile struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

func (m *Manager) SubtitleFiles(hash string) []SubtitleFile {
	return m.SubtitleFilesForFile(hash, -1)
}

func (m *Manager) SubtitleFilesForFile(hash string, fileIndex int) []SubtitleFile {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SubtitleFile{}
	j := m.jobs[hash]
	if j == nil || j.t.Info() == nil {
		return out
	}
	var targetStem string
	if fileIndex < 0 {
		fileIndex = j.record.FileIndex
	}
	files := j.t.Files()
	if fileIndex >= 0 && fileIndex < len(files) {
		base := filepath.Base(files[fileIndex].Path())
		targetStem = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	var matched, rest []SubtitleFile
	for i, f := range files {
		ext := strings.ToLower(filepath.Ext(f.Path()))
		if (ext == ".srt" || ext == ".vtt") && f.Length() > 0 && f.Length() <= subtitles.MaxSize {
			sf := SubtitleFile{i, f.DisplayPath()}
			if targetStem != "" && strings.Contains(strings.ToLower(f.DisplayPath()), targetStem) {
				matched = append(matched, sf)
			} else {
				rest = append(rest, sf)
			}
		}
	}
	out = append(out, matched...)
	out = append(out, rest...)
	return out
}
func (m *Manager) ReadSubtitle(ctx context.Context, hash string, index int) (string, []byte, error) {
	m.mu.Lock()
	j := m.jobs[hash]
	if j == nil || j.t.Info() == nil || j.record.Paused || index < 0 || index >= len(j.t.Files()) {
		m.mu.Unlock()
		return "", nil, ErrNotReady
	}
	f := j.t.Files()[index]
	ext := strings.ToLower(filepath.Ext(f.Path()))
	if (ext != ".srt" && ext != ".vtt") || f.Length() > subtitles.MaxSize || len(j.readers) >= 12 {
		m.mu.Unlock()
		return "", nil, errors.New("subtitle file unavailable")
	}
	ctx, cancel := context.WithCancel(ctx)
	reader := f.NewReader()
	reader.SetContext(ctx)
	reader.SetReadahead(f.Length())
	m.nextReader++
	id := m.nextReader
	j.readers[id] = cancel
	m.mu.Unlock()
	defer func() { cancel(); reader.Close(); m.mu.Lock(); delete(j.readers, id); m.mu.Unlock() }()
	b, err := io.ReadAll(io.LimitReader(reader, subtitles.MaxSize+1))
	return f.DisplayPath(), b, err
}
