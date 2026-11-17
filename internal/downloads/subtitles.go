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
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SubtitleFile{}
	j := m.jobs[hash]
	if j == nil || j.t.Info() == nil {
		return out
	}
	for i, f := range j.t.Files() {
		ext := strings.ToLower(filepath.Ext(f.Path()))
		if (ext == ".srt" || ext == ".vtt") && f.Length() > 0 && f.Length() <= subtitles.MaxSize {
			out = append(out, SubtitleFile{i, f.DisplayPath()})
		}
	}
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
