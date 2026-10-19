// Package media wraps ffprobe / ffmpeg as sidecar processes to read tracks from
// local or remote (HTTP) container files such as MKV / MP4, and to extract or
// convert subtitles into SRT. It deliberately never walks directory trees.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gawain12/BabelSub/pkg/lang"
	"github.com/Gawain12/BabelSub/pkg/syncer"
)

type Tools struct {
	FFmpeg  string
	FFprobe string
}

func NewTools(ffmpeg, ffprobe string) *Tools {
	if ffmpeg == "" {
		ffmpeg = syncer.FindExecutable("ffmpeg")
	}
	if ffprobe == "" {
		ffprobe = syncer.FindExecutable("ffprobe")
	}
	return &Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}
}

type Track struct {
	Index    int    `json:"index"`     // absolute stream index in the container
	Type     string `json:"type"`      // Video / Audio / Subtitle
	Codec    string `json:"codec"`     // e.g. subrip, ass, hdmv_pgs_subtitle
	Language string `json:"language"`  // canonical code when known
	RawLang  string `json:"raw_lang"`  // as stored in the container
	Title    string `json:"title"`     // track title tag
	Default  bool   `json:"default"`   // default disposition
	Forced   bool   `json:"forced"`    // forced disposition
	TextBase bool   `json:"text_base"` // subtitle can be converted to SRT without OCR
	Channels int    `json:"channels,omitempty"`
}

type ProbeResult struct {
	Path        string  `json:"path"`
	Format      string  `json:"format"`
	DurationSec float64 `json:"duration_sec"`
	SizeBytes   int64   `json:"size_bytes"`
	Tracks      []Track `json:"tracks"`
}

var textSubCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true, "webvtt": true,
	"mov_text": true, "text": true, "microdvd": true, "subviewer": true,
}

type ffprobeOut struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		Size       string `json:"size"`
	} `json:"format"`
	Streams []struct {
		Index       int               `json:"index"`
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		Channels    int               `json:"channels"`
		Tags        map[string]string `json:"tags"`
		Disposition map[string]int    `json:"disposition"`
	} `json:"streams"`
}

// Probe reads the container header. For HTTP sources ffprobe only issues range
// requests for the header, so this is cheap even for multi-GB files.
func (t *Tools) Probe(ctx context.Context, path string) (*ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("ffprobe 失败: %s", msg)
	}
	var raw ffprobeOut
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	res := &ProbeResult{Path: path, Format: raw.Format.FormatName}
	fmt.Sscan(raw.Format.Duration, &res.DurationSec)
	fmt.Sscan(raw.Format.Size, &res.SizeBytes)
	for _, s := range raw.Streams {
		var typ string
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue
			}
			typ = "Video"
		case "audio":
			typ = "Audio"
		case "subtitle":
			typ = "Subtitle"
		default:
			continue
		}
		rawLang := s.Tags["language"]
		if rawLang == "" {
			rawLang = s.Tags["LANGUAGE"]
		}
		title := s.Tags["title"]
		if title == "" {
			title = s.Tags["TITLE"]
		}
		code := lang.Normalize(rawLang)
		if code == "und" {
			code = ""
		}
		res.Tracks = append(res.Tracks, Track{
			Index:    s.Index,
			Type:     typ,
			Codec:    s.CodecName,
			Language: code,
			RawLang:  rawLang,
			Title:    title,
			Default:  s.Disposition["default"] == 1,
			Forced:   s.Disposition["forced"] == 1,
			TextBase: typ == "Subtitle" && textSubCodecs[s.CodecName],
			Channels: s.Channels,
		})
	}
	return res, nil
}

func (t *Tools) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, t.FFmpeg, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg 失败: %s", msg)
	}
	return nil
}

// ExtractSubtitle extracts one embedded subtitle stream as SRT. Note that the
// whole container has to be read because subtitle packets are interleaved.
func (t *Tools) ExtractSubtitle(ctx context.Context, mediaPath string, streamIndex int, outSRT string) error {
	if err := t.run(ctx, "-i", mediaPath, "-map", fmt.Sprintf("0:%d", streamIndex), "-c:s", "srt", "-f", "srt", outSRT); err != nil {
		return err
	}
	return nonEmpty(outSRT)
}

// ConvertToSRT converts any text subtitle (ass / ssa / vtt / srt in a legacy
// encoding) into UTF-8 SRT.
func (t *Tools) ConvertToSRT(ctx context.Context, in, outSRT string) error {
	args := []string{}
	if enc := DetectLegacyEncoding(in); enc != "" {
		args = append(args, "-sub_charenc", enc)
	}
	args = append(args, "-i", in, "-c:s", "srt", "-f", "srt", outSRT)
	if err := t.run(ctx, args...); err != nil {
		return err
	}
	return nonEmpty(outSRT)
}

func nonEmpty(p string) error {
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("输出字幕为空")
	}
	return nil
}

// DetectLegacyEncoding returns a likely legacy encoding for non-UTF-8 subtitle
// files (common for Chinese subtitles saved as GBK / Big5).
func DetectLegacyEncoding(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if bytes.HasPrefix(buf, []byte{0xEF, 0xBB, 0xBF}) || bytes.HasPrefix(buf, []byte{0xFF, 0xFE}) || bytes.HasPrefix(buf, []byte{0xFE, 0xFF}) {
		return ""
	}
	if validUTF8Prefix(buf) {
		return ""
	}
	return "GB18030"
}

func validUTF8Prefix(b []byte) bool {
	// A read boundary may cut a multi-byte rune; tolerate up to 3 trailing bytes.
	for cut := 0; cut <= 3 && cut <= len(b); cut++ {
		if utf8.Valid(b[:len(b)-cut]) {
			return true
		}
	}
	return false
}

var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".mov": true, ".avi": true, ".ts": true, ".m2ts": true, ".webm": true, ".wmv": true, ".flv": true, ".iso": false}
var subExts = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".sub": false}

func IsVideo(p string) bool    { return videoExts[strings.ToLower(filepath.Ext(p))] }
func IsSubtitle(p string) bool { return subExts[strings.ToLower(filepath.Ext(p))] }

type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Kind  string `json:"kind"` // dir | video | subtitle
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
}

// ListDir lists a single directory (non-recursive) keeping only folders, videos
// and subtitles. Hidden entries are skipped.
func ListDir(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		e := Entry{Name: name, Path: filepath.Join(dir, name)}
		switch {
		case de.IsDir():
			e.Kind = "dir"
		case IsVideo(name):
			e.Kind = "video"
		case IsSubtitle(name):
			e.Kind = "subtitle"
		default:
			continue
		}
		if info, err := de.Info(); err == nil {
			e.Size = info.Size()
			e.MTime = info.ModTime().Format(time.RFC3339)
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Kind == "dir") != (out[j].Kind == "dir") {
			return out[i].Kind == "dir"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

type Sidecar struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Language string `json:"language"` // guessed from the filename suffix
}

// Sidecars returns subtitle files next to a video that share its base name,
// e.g. "Movie.ja.srt" / "Movie.chs.ass" for "Movie.mkv". Only the video's own
// directory is read.
func Sidecars(videoPath string) []Sidecar {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Sidecar
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !IsSubtitle(name) || !strings.HasPrefix(name, base) {
			continue
		}
		out = append(out, Sidecar{Path: filepath.Join(dir, name), Name: name, Language: GuessLangFromName(name)})
	}
	return out
}

// GuessLangFromName inspects dot-separated filename parts such as
// "movie.zh-ja.ass" or "movie.chs.srt".
func GuessLangFromName(name string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))), ".")
	// "movie.ja.srt" → inspect the suffixes; a bare "ja.srt" counts too.
	first := 1
	if len(parts) == 1 {
		first = 0
	}
	for i := len(parts) - 1; i >= first && i >= len(parts)-3; i-- {
		p := parts[i]
		if i > 0 && (strings.Contains(p, "-") || strings.Contains(p, "&")) {
			return "bilingual"
		}
		if info := lang.Lookup(p); len(info.Countries) > 0 {
			return info.Code
		}
		switch p {
		case "sc", "tc", "gb", "big5", "简体", "繁体", "简中", "繁中":
			return "zh"
		}
	}
	return ""
}
