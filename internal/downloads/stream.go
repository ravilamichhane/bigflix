package downloads

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Single-range responses keep each browser request tied to one independent reader.
func ParseRange(raw string, size int64) (start, end int64, partial bool, err error) {
	start, end = 0, size-1
	if size <= 0 {
		return 0, 0, false, fmt.Errorf("empty file")
	}
	if raw == "" {
		return
	}
	partial = true
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		err = fmt.Errorf("unsupported range")
		return
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
	if len(parts) != 2 {
		err = fmt.Errorf("invalid range")
		return
	}
	if parts[0] == "" {
		n, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || n <= 0 {
			return 0, 0, true, fmt.Errorf("invalid suffix")
		}
		start = max(0, size-n)
		return
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, true, fmt.Errorf("range outside file")
	}
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, true, fmt.Errorf("invalid range end")
		}
		end = min(end, size-1)
	}
	return
}
func (m *Manager) Stream(w http.ResponseWriter, r *http.Request, hash string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	fileIndex := -1
	if rawFile := strings.TrimSpace(r.URL.Query().Get("file")); rawFile != "" {
		if idx, err := strconv.Atoi(rawFile); err == nil && idx >= 0 {
			fileIndex = idx
		}
	}
	reader, file, release, err := m.ReaderForFile(ctx, hash, fileIndex)
	if err != nil {
		status := http.StatusConflict
		if err == ErrNotFound {
			status = 404
		}
		http.Error(w, err.Error(), status)
		return
	}
	defer release()
	start, end, partial, err := ParseRange(r.Header.Get("Range"), file.Size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", file.Size))
		w.WriteHeader(416)
		return
	}
	contentType := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(file.Name)) {
	case ".mp4", ".m4v":
		contentType = "video/mp4"
	case ".webm":
		contentType = "video/webm"
	case ".mkv":
		contentType = "video/x-matroska"
	case ".mov":
		contentType = "video/quicktime"
	case ".ts", ".m2ts":
		contentType = "video/mp2t"
	case ".avi":
		contentType = "video/x-msvideo"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	status := 200
	if partial {
		status = 206
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.Size))
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="bigflix`+strings.ToLower(filepath.Ext(file.Name))+`"`)
	}
	if r.Method == "HEAD" {
		w.WriteHeader(status)
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})
	if _, err = reader.Seek(start, io.SeekStart); err != nil {
		http.Error(w, "Unable to seek", 500)
		return
	}
	// Cancel stalled torrent reads only while waiting on reader.Read, not during
	// client write backpressure (client disconnects cancel r.Context()).
	idle := time.AfterFunc(45*time.Second, cancel)
	defer idle.Stop()
	buf := make([]byte, 256<<10)
	left := end - start + 1
	sent := false
	for left > 0 {
		idle.Reset(45 * time.Second)
		n, readErr := reader.Read(buf[:min(int64(len(buf)), left)])
		idle.Stop()
		if n > 0 {
			if !sent {
				w.WriteHeader(status)
				sent = true
			}
			if _, err = w.Write(buf[:n]); err != nil {
				return
			}
			left -= int64(n)
		}
		if readErr != nil || n == 0 {
			if !sent {
				w.Header().Del("Content-Length")
				w.Header().Del("Content-Range")
				w.Header().Set("Retry-After", "3")
				http.Error(w, "Waiting for torrent pieces. Retry playback when more peers or data are available.", 503)
			}
			return
		}
	}
}
