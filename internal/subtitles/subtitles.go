package subtitles

import (
	"bigflix/internal/database"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxSize = 2 << 20

var timing = regexp.MustCompile(`^(\d{1,3}:\d{2}:\d{2}[.,]\d{3}|\d{2}:\d{2}\.\d{3})\s+-->\s+(\d{1,3}:\d{2}:\d{2}[.,]\d{3}|\d{2}:\d{2}\.\d{3})(?:\s+.*)?$`)
var languageRE = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

func ValidLanguage(s string) bool { return languageRE.MatchString(s) }
func timestamp(raw string) (int64, error) {
	parts := strings.Split(strings.ReplaceAll(raw, ",", "."), ":")
	if len(parts) == 2 {
		parts = append([]string{"0"}, parts...)
	}
	h, _ := strconv.ParseInt(parts[0], 10, 64)
	m, _ := strconv.ParseInt(parts[1], 10, 64)
	sec := strings.Split(parts[2], ".")
	s, _ := strconv.ParseInt(sec[0], 10, 64)
	ms, _ := strconv.ParseInt(sec[1], 10, 64)
	if m > 59 || s > 59 {
		return 0, errors.New("invalid subtitle timestamp")
	}
	return ((h*60+m)*60+s)*1000 + ms, nil
}
func stamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// Normalize emits only timing and escaped cue text; STYLE/REGION blocks and
// provider markup cannot inject styles or HTML into the player.
func Normalize(b []byte) ([]byte, error) {
	if len(b) > MaxSize || !utf8.Valid(b) {
		return nil, errors.New("subtitle must be UTF-8 SRT/VTT, at most 2 MB")
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(string(b), "\ufeff"), "\r\n", "\n"), "\r", "\n"), "\n")
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	count := 0
	for i := 0; i < len(lines); i++ {
		match := timing.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if match == nil {
			continue
		}
		start, err := timestamp(match[1])
		if err != nil {
			return nil, err
		}
		end, err := timestamp(match[2])
		if err != nil || end <= start {
			return nil, errors.New("invalid subtitle cue duration")
		}
		var text []string
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
			i++
			text = append(text, html.EscapeString(strings.ReplaceAll(lines[i], "\x00", "")))
		}
		if len(text) == 0 {
			continue
		}
		out.WriteString(stamp(start) + " --> " + stamp(end) + "\n" + strings.Join(text, "\n") + "\n\n")
		count++
	}
	if count == 0 {
		return nil, errors.New("no valid subtitle cues found; upload an SRT or WebVTT file")
	}
	return []byte(out.String()), nil
}
func Save(ctx context.Context, db *database.Store, hash, label, language string, b []byte) (database.Subtitle, error) {
	if !ValidLanguage(language) {
		return database.Subtitle{}, errors.New("choose a valid subtitle language")
	}
	vtt, err := Normalize(b)
	if err != nil {
		return database.Subtitle{}, err
	}
	if len(label) > 180 {
		label = string([]rune(label)[:min(100, len([]rune(label)))])
	}
	sum := sha256.Sum256(append([]byte(language+"\n"), vtt...))
	sub := database.Subtitle{ID: hex.EncodeToString(sum[:]), Label: label, Language: language}
	return sub, db.SaveSubtitle(ctx, hash, sub, vtt)
}
