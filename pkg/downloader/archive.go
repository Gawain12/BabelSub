package downloader

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

var subtitleExts = map[string]int{".srt": 4, ".ass": 3, ".ssa": 3, ".vtt": 2}

func isSubtitleExt(ext string) bool { return subtitleExts[strings.ToLower(ext)] > 0 }

func isZip(b []byte) bool { return bytes.HasPrefix(b, []byte("PK\x03\x04")) }
func isRar(b []byte) bool { return bytes.HasPrefix(b, []byte("Rar!\x1a\x07")) }
func is7z(b []byte) bool  { return bytes.HasPrefix(b, []byte("7z\xbc\xaf\x27\x1c")) }

// Name fragments used by Chinese fansub groups to tag subtitle variants.
var (
	simplifiedTags  = []string{"chs", "简", "gb", ".sc.", "_sc", "-sc", "zh-cn", "zh-hans", "zho"}
	traditionalTags = []string{"cht", "繁", "big5", ".tc.", "_tc", "-tc", "zh-tw", "zh-hant"}
	bilingualTags   = []string{"双语", "雙語", "&", "chs.eng", "chs_eng", "chs+eng", "简英", "简日", "中英", "中日"}
)

// subtitleScore ranks candidate files inside an archive / ASSRT file list.
// Lower than zero means "not a subtitle". Plain simplified-Chinese SRT wins,
// since ASSRT archives are used as the Chinese half of the bilingual file.
func subtitleScore(name string) int {
	ext := strings.ToLower(path.Ext(name))
	base := subtitleExts[ext]
	if base == 0 || strings.HasPrefix(path.Base(name), "._") || strings.Contains(name, "__MACOSX") {
		return -1
	}
	n := strings.ToLower(name)
	score := base
	if containsAny(n, simplifiedTags) {
		score += 4
	}
	if containsAny(n, traditionalTags) {
		score -= 2
	}
	if containsAny(n, bilingualTags) {
		score -= 3
	}
	return score
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// bestSubtitle returns the index of the best candidate name, or -1.
func bestSubtitle(names []string) int {
	best, bestScore := -1, -1
	for i, n := range names {
		if s := subtitleScore(n); s > bestScore {
			best, bestScore = i, s
		}
	}
	return best
}

// extractZip writes the best subtitle inside a zip archive to dstBase+ext.
func extractZip(data []byte, dstBase string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("无法解压: %w", err)
	}
	var names []string
	var files []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		names = append(names, f.Name)
		files = append(files, f)
	}
	i := bestSubtitle(names)
	if i < 0 {
		return "", fmt.Errorf("压缩包内没有 srt / ass 字幕 (%d 个文件)", len(names))
	}
	rc, err := files[i].Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, maxDownload))
	if err != nil {
		return "", err
	}
	out := dstBase + strings.ToLower(path.Ext(names[i]))
	if err := os.WriteFile(out, body, 0644); err != nil {
		return "", err
	}
	return out, nil
}
