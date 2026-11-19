package web

import (
	"bigflix/internal/downloads"
	"bigflix/internal/subtitles"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type embeddedSubtitle struct {
	StreamIndex     int    `json:"stream_index"`
	Language        string `json:"language"`
	Label           string `json:"label"`
	Codec           string `json:"codec"`
	Default         bool   `json:"default"`
	Forced          bool   `json:"forced"`
	HearingImpaired bool   `json:"hearing_impaired"`
}

// Media probe results for containers the browser cannot open directly.
type mediaProbe struct {
	Duration  float64            `json:"duration"`
	Video     string             `json:"video"`
	Codecs    string             `json:"codecs"`
	Width     int                `json:"width"`
	Height    int                `json:"height"`
	Audio     string             `json:"audio"`
	Subtitles []embeddedSubtitle `json:"subtitles,omitempty"`
}

type activeStream struct {
	id     uint64
	cancel context.CancelFunc
}

var (
	probeMu          sync.Mutex
	probeCache       = map[string]mediaProbe{}
	remuxMu          sync.Mutex
	activeRemux      = map[string]activeStream{}
	remuxSeq         uint64
	encoderOne       sync.Once
	h264Enc          = "libx264"
	vaapiDevice      = ""
	hasReadrateBurst = false
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
	key := strings.ToLower(hash)
	prefix := key + "/"
	probeMu.Lock()
	for k := range probeCache {
		if strings.HasPrefix(k, prefix) {
			delete(probeCache, k)
		}
	}
	probeMu.Unlock()
	remuxMu.Lock()
	if prev, ok := activeRemux[key]; ok {
		prev.cancel()
		delete(activeRemux, key)
	}
	remuxMu.Unlock()
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
		"-show_entries", "format=duration:stream=index,codec_type,codec_name,profile,width,height,level:stream_tags=language,title:stream_disposition=default,forced,hearing_impaired",
		"-of", "json", src).Output()
	if err != nil {
		return mediaProbe{}, errProbeWait
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index       int               `json:"index"`
			Type        string            `json:"codec_type"`
			Codec       string            `json:"codec_name"`
			Profile     string            `json:"profile"`
			Width       int               `json:"width"`
			Height      int               `json:"height"`
			Level       int               `json:"level"`
			Tags        map[string]string `json:"tags"`
			Disposition struct {
				Default         int `json:"default"`
				Forced          int `json:"forced"`
				HearingImpaired int `json:"hearing_impaired"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
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
		case st.Type == "subtitle" && isTextSubtitleCodec(st.Codec):
			langRaw := tagCaseInsensitive(st.Tags, "language")
			title := strings.TrimSpace(tagCaseInsensitive(st.Tags, "title"))
			code, langName := normalizeSubLang(langRaw)
			label := buildEmbeddedSubLabel(langName, title, st.Index, st.Disposition.Forced == 1, st.Disposition.HearingImpaired == 1)
			p.Subtitles = append(p.Subtitles, embeddedSubtitle{
				StreamIndex:     st.Index,
				Language:        code,
				Label:           label,
				Codec:           st.Codec,
				Default:         st.Disposition.Default == 1,
				Forced:          st.Disposition.Forced == 1,
				HearingImpaired: st.Disposition.HearingImpaired == 1,
			})
		}
	}
	probeMu.Lock()
	probeCache[key] = p
	probeMu.Unlock()
	return p, nil
}

func tagCaseInsensitive(tags map[string]string, key string) string {
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func isTextSubtitleCodec(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "subrip", "srt", "ass", "ssa", "webvtt", "mov_text", "text", "ttml",
		"sami", "microdvd", "realtext", "jacosub", "mpl2", "pjs", "stl",
		"subviewer", "subviewer1", "vplayer", "eia_608", "eia_708":
		return true
	default:
		return false
	}
}

func normalizeSubLang(raw string) (code, name string) {
	c := strings.ToLower(strings.TrimSpace(raw))
	switch c {
	case "en", "eng":
		return "en", "English"
	case "es", "spa":
		return "es", "Spanish"
	case "fr", "fre", "fra":
		return "fr", "French"
	case "de", "ger", "deu":
		return "de", "German"
	case "hi", "hin":
		return "hi", "Hindi"
	case "ne", "nep":
		return "ne", "Nepali"
	case "pt", "por", "pt-br":
		return "pt-br", "Portuguese"
	case "ar", "ara":
		return "ar", "Arabic"
	case "ja", "jpn":
		return "ja", "Japanese"
	case "ko", "kor":
		return "ko", "Korean"
	case "zh", "zho", "chi", "zh-cn":
		return "zh-cn", "Chinese"
	case "it", "ita":
		return "it", "Italian"
	case "ru", "rus":
		return "ru", "Russian"
	case "nl", "dut", "nld":
		return "nl", "Dutch"
	case "pl", "pol":
		return "pl", "Polish"
	case "tr", "tur":
		return "tr", "Turkish"
	case "sv", "swe":
		return "sv", "Swedish"
	case "no", "nor", "nob":
		return "no", "Norwegian"
	case "da", "dan":
		return "da", "Danish"
	case "fi", "fin":
		return "fi", "Finnish"
	case "id", "ind":
		return "id", "Indonesian"
	case "vi", "vie":
		return "vi", "Vietnamese"
	case "th", "tha":
		return "th", "Thai"
	case "", "und":
		return "und", "Unknown"
	default:
		if subtitles.ValidLanguage(c) {
			return c, strings.ToUpper(c)
		}
		return "und", "Unknown"
	}
}

func buildEmbeddedSubLabel(langName, title string, streamIndex int, forced, sdh bool) string {
	var base string
	switch {
	case title != "" && langName != "" && langName != "Unknown" && !strings.Contains(strings.ToLower(title), strings.ToLower(langName)):
		base = langName + " · " + title
	case title != "":
		base = title
	case langName != "" && langName != "Unknown":
		base = langName
	default:
		base = fmt.Sprintf("Track #%d", streamIndex)
	}
	if sdh && !strings.Contains(strings.ToLower(base), "sdh") {
		base += " · SDH"
	}
	if forced && !strings.Contains(strings.ToLower(base), "forced") {
		base += " · Forced"
	}
	return base
}

func (a *App) cachedEmbeddedSubtitles(ctx context.Context, hash string) []embeddedSubtitle {
	s, _, err := a.remuxSourceCtx(ctx, hash)
	if err != nil || s.Size <= 0 || s.Paused {
		return nil
	}
	key := fmt.Sprintf("%s/%d", s.Hash, s.FileIndex)
	probeMu.Lock()
	p, cached := probeCache[key]
	probeMu.Unlock()
	if cached {
		return p.Subtitles
	}
	// Quick opportunistic probe so built-in subtitles appear even before pressing Play.
	pctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	p, err = a.probeMedia(pctx, hash)
	if err != nil {
		return nil
	}
	return p.Subtitles
}

func (a *App) extractEmbeddedSubtitle(ctx context.Context, hash string, streamIndex int) (embeddedSubtitle, []byte, error) {
	s, src, err := a.remuxSourceCtx(ctx, hash)
	if err != nil {
		return embeddedSubtitle{}, nil, err
	}
	p, err := a.probeMedia(ctx, hash)
	if err != nil {
		return embeddedSubtitle{}, nil, err
	}
	var target *embeddedSubtitle
	for i := range p.Subtitles {
		if p.Subtitles[i].StreamIndex == streamIndex {
			target = &p.Subtitles[i]
			break
		}
	}
	if target == nil {
		return embeddedSubtitle{}, nil, errors.New("built-in subtitle track not found")
	}

	timeout := 35 * time.Second
	if s.Size > 0 && s.Completed >= s.Size {
		timeout = 90 * time.Second
	}
	ectx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-rw_timeout", "30000000",
		"-reconnect", "1", "-reconnect_on_network_error", "1", "-reconnect_delay_max", "5",
		"-i", src,
		"-map", fmt.Sprintf("0:%d", target.StreamIndex),
		"-vn", "-an", "-dn",
		"-c:s", "webvtt",
		"-f", "webvtt",
		"pipe:1",
	}
	cmd := exec.CommandContext(ectx, downloads.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return embeddedSubtitle{}, nil, errors.New("unable to start subtitle extraction")
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return embeddedSubtitle{}, nil, errors.New("unable to start subtitle extraction")
	}
	var buf bytes.Buffer
	_, readErr := io.Copy(&buf, io.LimitReader(stdout, subtitles.MaxSize+1))
	waitErr := cmd.Wait()

	if readErr != nil || waitErr != nil {
		if ectx.Err() == context.DeadlineExceeded && s.Completed < s.Size {
			return embeddedSubtitle{}, nil, fmt.Errorf(
				"built-in subtitles are interleaved through the video and need the download to finish first (currently %.0f%%) — or search online",
				s.Progress*100,
			)
		}
		slog.Warn("embedded subtitle extraction failed", "hash", s.Hash, "stream", streamIndex, "error", waitErr, "stderr", stderr.String())
		return embeddedSubtitle{}, nil, errors.New("could not extract built-in subtitle track")
	}
	if buf.Len() > subtitles.MaxSize {
		return embeddedSubtitle{}, nil, errors.New("built-in subtitle track exceeds 2 MB limit")
	}
	return *target, buf.Bytes(), nil
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

func scaledHeight(srcW, srcH, maxH int) int {
	if maxH <= 0 {
		return srcH
	}
	if srcW <= 0 || srcH <= 0 {
		return maxH
	}
	maxW := maxH * 16 / 9
	scale := min(1.0, min(float64(maxW)/float64(srcW), float64(maxH)/float64(srcH)))
	h := int(float64(srcH)*scale+0.5) &^ 1
	if h < 2 {
		return 2
	}
	return h
}

func detectEncoders() {
	encoderOne.Do(func() {
		out, _ := exec.Command(downloads.FFmpeg, "-hide_banner", "-encoders").Output()
		encs := string(out)
		switch {
		case strings.Contains(encs, "h264_videotoolbox"):
			h264Enc = "h264_videotoolbox"
		case strings.Contains(encs, "h264_nvenc") && deviceReadable("/dev/nvidia0"):
			h264Enc = "h264_nvenc"
		case strings.Contains(encs, "h264_vaapi") && deviceReadable("/dev/dri/renderD128"):
			h264Enc = "h264_vaapi"
			vaapiDevice = "/dev/dri/renderD128"
		}
		help, _ := exec.Command(downloads.FFmpeg, "-hide_banner", "-h", "long").Output()
		hasReadrateBurst = strings.Contains(string(help), "readrate_initial_burst")
	})
}

func deviceReadable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func ffmpegThreads() int {
	if raw := strings.TrimSpace(os.Getenv("FFMPEG_THREADS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 32 {
			return n
		}
	}
	n := runtime.NumCPU() / 2
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

func ffmpegPreset() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FFMPEG_PRESET"))) {
	case "ultrafast", "superfast", "veryfast", "faster", "fast":
		return strings.ToLower(strings.TrimSpace(os.Getenv("FFMPEG_PRESET")))
	default:
		return "ultrafast"
	}
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

	// Cancel any stale FFmpeg stream for this download immediately (prevents
	// stacked encoders burning CPU behind reverse proxies on seek/quality switches).
	rctx, cancel := context.WithCancel(r.Context())
	remuxMu.Lock()
	remuxSeq++
	myID := remuxSeq
	if prev, ok := activeRemux[s.Hash]; ok {
		prev.cancel()
	}
	activeRemux[s.Hash] = activeStream{id: myID, cancel: cancel}
	remuxMu.Unlock()
	defer func() {
		cancel()
		remuxMu.Lock()
		if cur, ok := activeRemux[s.Hash]; ok && cur.id == myID {
			delete(activeRemux, s.Hash)
		}
		remuxMu.Unlock()
	}()

	start, _ := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	start = max(0, start)
	compat := r.URL.Query().Get("mode") == "compat"
	maxH, _ := strconv.Atoi(r.URL.Query().Get("maxh"))
	switch maxH {
	case 480, 720, 1080, 1440, 2160:
	default:
		maxH = 0
	}
	probeMu.Lock()
	p := probeCache[fmt.Sprintf("%s/%d", s.Hash, s.FileIndex)]
	probeMu.Unlock()

	// Default compat transcode to <=1080p when source is >1080p so high-res
	// fallback runs faster than real time and stays within browser MSE quotas.
	if compat && maxH == 0 && (p.Height > 1080 || p.Width > 1920) {
		maxH = 1080
	}

	// In copy mode the MP4 muxer rebases output timestamps to zero, so seek to
	// an exact keyframe and tell the client where the stream begins.
	if start > 0 && !compat {
		start = keyframeBefore(rctx, src, start)
	}
	detectEncoders()

	vtHardware := compat && h264Enc == "h264_videotoolbox" && p.Height > 0 && (p.Video == "hevc" || p.Video == "h264")
	vaapiHardware := compat && h264Enc == "h264_vaapi" && vaapiDevice != ""
	nvencHardware := compat && h264Enc == "h264_nvenc"
	swCompat := compat && !vtHardware && !vaapiHardware && !nvencHardware
	threads := ffmpegThreads()

	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if vtHardware {
		args = append(args, "-hwaccel", "videotoolbox", "-hwaccel_output_format", "videotoolbox_vld")
	} else if vaapiHardware {
		args = append(args, "-vaapi_device", vaapiDevice)
	} else if swCompat {
		args = append(args, "-threads", strconv.Itoa(threads))
		if hasReadrateBurst {
			// Burst 8s immediately for instant start/seek, then pace smoothly at 2x
			// so software transcoding doesn't peg CPU at 100% or over-buffer behind proxies.
			args = append(args, "-readrate", "2.0", "-readrate_initial_burst", "8")
		}
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
		outH := scaledHeight(p.Width, p.Height, maxH)
		rate, maxRate, bufSize := "6M", "8M", "12M"
		switch {
		case outH > 0 && outH <= 480:
			rate, maxRate, bufSize = "1800k", "2500k", "4M"
		case outH > 0 && outH <= 720:
			rate, maxRate, bufSize = "3500k", "5M", "8M"
		case outH > 1440:
			rate, maxRate, bufSize = "16M", "22M", "32M"
		case outH > 1080:
			rate, maxRate, bufSize = "10M", "14M", "20M"
		}

		switch {
		case vtHardware:
			args = append(args,
				"-vf", fmt.Sprintf("scale_vt=w=-2:h=%d", outH),
				"-c:v", "h264_videotoolbox",
				"-b:v", rate, "-maxrate", maxRate, "-bufsize", bufSize,
				"-prio_speed", "1", "-profile:v", "high", "-g", "48",
			)
		case vaapiHardware:
			vf := "format=nv12,hwupload"
			if outH > 0 && (p.Height == 0 || outH < p.Height) {
				vf = fmt.Sprintf("format=nv12,hwupload,scale_vaapi=w=-2:h=%d", outH)
			}
			args = append(args,
				"-vf", vf,
				"-c:v", "h264_vaapi",
				"-b:v", rate, "-maxrate", maxRate, "-bufsize", bufSize, "-g", "60",
			)
		case nvencHardware:
			vf := "format=yuv420p"
			if outH > 0 && (p.Height == 0 || outH < p.Height) {
				vf = fmt.Sprintf("scale=-2:%d:flags=fast_bilinear,format=yuv420p", outH)
			}
			args = append(args,
				"-vf", vf,
				"-c:v", "h264_nvenc", "-preset", "p1", "-tune", "ll",
				"-b:v", rate, "-maxrate", maxRate, "-bufsize", bufSize, "-g", "60",
			)
		default:
			vf := "format=yuv420p"
			if outH > 0 && (p.Height == 0 || outH < p.Height) {
				vf = fmt.Sprintf("scale=-2:%d:flags=fast_bilinear,format=yuv420p", outH)
			}
			args = append(args,
				"-vf", vf,
				"-c:v", "libx264", "-preset", ffmpegPreset(), "-tune", "zerolatency",
				"-crf", "24", "-maxrate", maxRate, "-bufsize", bufSize,
				"-pix_fmt", "yuv420p", "-g", "60", "-threads", strconv.Itoa(threads),
			)
		}
	} else {
		args = append(args, "-c:v", "copy")
		if p.Video == "hevc" {
			args = append(args, "-tag:v", "hvc1")
		}
	}
	if p.Audio == "aac" {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
	}
	args = append(args,
		"-avoid_negative_ts", "make_zero",
		"-f", "mp4", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "pipe:1")

	cmd := exec.CommandContext(rctx, downloads.FFmpeg, args...)
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
		if err := cmd.Wait(); err != nil && rctx.Err() == nil {
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
