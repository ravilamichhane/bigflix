package web

import (
	"bigflix/internal/downloads"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Media probe results for containers the browser cannot open directly.
type mediaProbe struct {
	Duration float64 `json:"duration"`
	Video    string  `json:"video"`
	Codecs   string  `json:"codecs"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Audio    string  `json:"audio"`
}

var (
	probeMu    sync.Mutex
	probeCache = map[string]mediaProbe{}
	encoderOne sync.Once
	h264Enc    = "libx264"
)

var (
	errFFmpegMissing = errors.New("FFmpeg is required to play this video in the browser")
	errNoLocalAddr   = errors.New("unable to resolve local media address")
	errProbeWait     = errors.New("waiting for enough video data to read the file header")
	errProbeDecode   = errors.New("unreadable media information")
)

func (a *App) remuxRoutes(mux *http.ServeMux) {
	mux.Handle("GET /media/{hash}/remux", a.requireMediaAuth(http.HandlerFunc(a.remux)))
}

func clearProbeCache(hash string) {
	prefix := strings.ToLower(hash) + "/"
	probeMu.Lock()
	for k := range probeCache {
		if strings.HasPrefix(k, prefix) {
			delete(probeCache, k)
		}
	}
	probeMu.Unlock()
}

// selfURL points ffmpeg back at the byte-range media route on the address the
// request arrived on, so torrent piece prioritization keeps working.
func (a *App) selfURL(ctx context.Context, hash string) (string, bool) {
	addr, ok := ctx.Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return "", false
	}
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "", false
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	u := "http://" + net.JoinHostPort(host, port) + "/media/" + hash
	if token := a.getToken(); token != "" {
		u += "?token=" + url.QueryEscape(token)
	}
	return u, true
}

func (a *App) remuxSourceCtx(ctx context.Context, hash string) (downloads.Status, string, error) {
	if a.engine == nil || downloads.FFmpeg == "" {
		return downloads.Status{}, "", errFFmpegMissing
	}
	s, err := a.engine.Get(hash)
	if err != nil {
		return s, "", downloads.ErrNotFound
	}
	src, ok := a.selfURL(ctx, s.Hash)
	if !ok {
		return s, "", errNoLocalAddr
	}
	return s, src, nil
}

func (a *App) probeMedia(ctx context.Context, hash string) (mediaProbe, error) {
	s, src, err := a.remuxSourceCtx(ctx, hash)
	if err != nil {
		return mediaProbe{}, err
	}
	key := fmt.Sprintf("%s/%d", s.Hash, s.FileIndex)
	probeMu.Lock()
	p, cached := probeCache[key]
	probeMu.Unlock()
	if cached {
		return p, nil
	}
	ffprobe := strings.TrimSuffix(downloads.FFmpeg, "ffmpeg") + "ffprobe"
	pctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, ffprobe, "-v", "error", "-rw_timeout", "60000000",
		"-show_entries", "format=duration:stream=codec_type,codec_name,profile,width,height,level",
		"-of", "json", src).Output()
	if err != nil {
		return mediaProbe{}, errProbeWait
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type, Codec, Profile string
			Width, Height, Level int
		} `json:"streams"`
	}
	if err := json.Unmarshal(fixKeys(out), &raw); err != nil {
		return mediaProbe{}, errProbeDecode
	}
	p.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, st := range raw.Streams {
		switch {
		case st.Type == "video" && p.Video == "":
			p.Video, p.Width, p.Height = st.Codec, st.Width, st.Height
			p.Codecs = codecString(st.Codec, st.Profile, st.Level)
		case st.Type == "audio" && p.Audio == "":
			p.Audio = st.Codec
		}
	}
	probeMu.Lock()
	probeCache[key] = p
	probeMu.Unlock()
	return p, nil
}

// fixKeys maps ffprobe's snake_case keys onto the anonymous struct fields.
func fixKeys(b []byte) []byte {
	return []byte(strings.NewReplacer(`"codec_type"`, `"Type"`, `"codec_name"`, `"Codec"`).Replace(string(b)))
}

func codecString(codec, profile string, level int) string {
	switch codec {
	case "hevc":
		if strings.Contains(profile, "10") {
			return fmt.Sprintf("hvc1.2.4.L%d.B0", level)
		}
		return fmt.Sprintf("hvc1.1.6.L%d.B0", level)
	case "h264":
		return "avc1.640033"
	case "av1":
		return "av01.0.08M.08"
	case "vp9":
		return "vp09.00.40.08"
	}
	return codec
}

func (a *App) remux(w http.ResponseWriter, r *http.Request) {
	s, src, err := a.remuxSourceCtx(r.Context(), r.PathValue("hash"))
	if err != nil {
		switch {
		case errors.Is(err, downloads.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, errFFmpegMissing):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	start, _ := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	start = max(0, start)
	compat := r.URL.Query().Get("mode") == "compat"
	probeMu.Lock()
	p := probeCache[fmt.Sprintf("%s/%d", s.Hash, s.FileIndex)]
	probeMu.Unlock()

	// In copy mode the MP4 muxer rebases output timestamps to zero, so seek to
	// an exact keyframe and tell the client where the stream begins.
	if start > 0 && !compat {
		start = keyframeBefore(r.Context(), src, start)
	}
	encoderOne.Do(func() {
		out, _ := exec.Command(downloads.FFmpeg, "-hide_banner", "-encoders").Output()
		if strings.Contains(string(out), "h264_videotoolbox") {
			h264Enc = "h264_videotoolbox"
		}
	})
	// On macOS, decode and encode on the media engine so a 4K HEVC 10-bit
	// conversion runs faster than real time without loading the CPU.
	hardware := compat && h264Enc == "h264_videotoolbox" && p.Height > 0 && (p.Video == "hevc" || p.Video == "h264")
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if hardware {
		args = append(args, "-hwaccel", "videotoolbox", "-hwaccel_output_format", "videotoolbox_vld")
	}
	args = append(args, "-rw_timeout", "60000000",
		"-reconnect", "1", "-reconnect_on_network_error", "1", "-reconnect_delay_max", "10")
	if !compat {
		args = append(args, "-noaccurate_seek")
	}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", src, "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-map_chapters", "-1")
	if compat {
		// Keep the source resolution; only the codec changes.
		filter := "format=yuv420p"
		if hardware {
			filter = fmt.Sprintf("scale_vt=w=-2:h=%d", p.Height)
		}
		args = append(args, "-vf", filter, "-c:v", h264Enc)
		if h264Enc == "h264_videotoolbox" {
			rate := "8M"
			if p.Height > 1440 {
				rate = "12M"
			}
			args = append(args, "-b:v", rate, "-prio_speed", "1", "-profile:v", "high")
		} else {
			args = append(args, "-preset", "veryfast", "-crf", "21")
		}
		args = append(args, "-g", "48")
	} else {
		args = append(args, "-c:v", "copy")
		if p.Video == "hevc" {
			args = append(args, "-tag:v", "hvc1")
		}
	}
	args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "192k",
		"-avoid_negative_ts", "make_zero",
		"-f", "mp4", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "pipe:1")

	cmd := exec.CommandContext(r.Context(), downloads.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "Unable to start remux", 500)
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		http.Error(w, "Unable to start remux", 500)
		return
	}
	defer func() {
		if err := cmd.Wait(); err != nil && r.Context().Err() == nil {
			slog.Warn("remux ended", "hash", s.Hash, "error", err, "stderr", stderr.String())
		}
	}()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("X-Stream-Start", strconv.FormatFloat(start, 'f', 3, 64))
	// The client applies backpressure while its buffer is full, so writes may
	// block for long periods. Client disconnects cancel the request context.
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})
	buf := make([]byte, 256<<10)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = controller.Flush()
		}
		if err != nil {
			return
		}
	}
}

func keyframeBefore(ctx context.Context, src string, t float64) float64 {
	ffprobe := strings.TrimSuffix(downloads.FFmpeg, "ffmpeg") + "ffprobe"
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-rw_timeout", "60000000", "-select_streams", "v:0",
		"-read_intervals", strconv.FormatFloat(t, 'f', 3, 64)+"%+#1", "-show_entries", "packet=pts_time", "-of", "csv=p=0", src).Output()
	if err != nil {
		return t
	}
	k, err := strconv.ParseFloat(strings.TrimSpace(strings.Split(string(out), "\n")[0]), 64)
	if err != nil || k > t {
		return t
	}
	return k
}
