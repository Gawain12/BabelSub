// Package pipeline turns two subtitle sources into one aligned bilingual ASS:
// resolve each source to a UTF-8 SRT, align with ffsubsync, then merge.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gawain12/BabelSub/pkg/ass"
	"github.com/Gawain12/BabelSub/pkg/config"
	"github.com/Gawain12/BabelSub/pkg/media"
	"github.com/Gawain12/BabelSub/pkg/syncer"
)

const (
	KindFile   = "file"   // a subtitle file on disk (srt / ass / ssa / vtt)
	KindTrack  = "track"  // an embedded subtitle stream of a media file or URL
	KindURL    = "url"    // a subtitle downloadable over HTTP (online sources)
	KindServer = "server" // a subtitle stream served by the media server

	SyncPrimary = "primary" // align the translation to the original-language subtitle
	SyncVideo   = "video"   // align both subtitles to the video's audio track
	SyncNone    = "none"    // subtitles are already in sync (e.g. tracks of the same MKV)
)

type Source struct {
	Kind        string `json:"kind"`
	Path        string `json:"path,omitempty"`
	Track       int    `json:"track,omitempty"`
	URL         string `json:"url,omitempty"`
	ItemID      string `json:"item_id,omitempty"`
	StreamIndex int    `json:"stream_index,omitempty"`
	Label       string `json:"label,omitempty"`
}

func (s Source) String() string {
	if s.Label != "" {
		return s.Label
	}
	switch s.Kind {
	case KindFile:
		return filepath.Base(s.Path)
	case KindTrack:
		return fmt.Sprintf("%s #%d", filepath.Base(s.Path), s.Track)
	case KindURL:
		return s.URL
	case KindServer:
		return fmt.Sprintf("媒体库字幕轨 #%d", s.StreamIndex)
	}
	return s.Kind
}

type Request struct {
	Primary   Source `json:"primary"`
	Secondary Source `json:"secondary"`
	SyncMode  string `json:"sync_mode"`
	VideoPath string `json:"video_path,omitempty"`
	OutPath   string `json:"out_path"`
}

type Env struct {
	Tools       *media.Tools
	Syncer      *syncer.Syncer
	Style       *config.StyleConfig
	Download    func(url, dstBase string) (string, error) // returns the written path (extension decided by the downloader)
	ServerFetch func(itemID string, streamIndex int) ([]byte, error)
	Log         func(format string, args ...any)
	Step        func(step int, total int, title string)
}

func (r *Request) Validate() error {
	if r.Primary.Kind == "" || r.Secondary.Kind == "" {
		return fmt.Errorf("需要同时指定原语字幕和中文字幕")
	}
	if r.OutPath == "" {
		return fmt.Errorf("缺少输出路径")
	}
	if !strings.EqualFold(filepath.Ext(r.OutPath), ".ass") {
		return fmt.Errorf("输出文件必须是 .ass")
	}
	switch r.SyncMode {
	case "", SyncPrimary, SyncNone:
	case SyncVideo:
		if r.VideoPath == "" {
			return fmt.Errorf("以视频音轨对轴需要提供视频文件")
		}
	default:
		return fmt.Errorf("未知对轴方式: %s", r.SyncMode)
	}
	return nil
}

// Run executes the request using workDir for intermediates and returns the
// written ASS path.
func Run(ctx context.Context, env Env, req Request, workDir string) (string, error) {
	if err := req.Validate(); err != nil {
		return "", err
	}
	if env.Log == nil {
		env.Log = func(string, ...any) {}
	}
	if env.Step == nil {
		env.Step = func(int, int, string) {}
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", err
	}

	total := 4
	if req.SyncMode == SyncNone {
		total = 3
	}

	env.Step(1, total, "准备原语字幕")
	prim, err := resolve(ctx, env, req.Primary, filepath.Join(workDir, "primary"))
	if err != nil {
		return "", fmt.Errorf("原语字幕: %w", err)
	}
	env.Step(2, total, "准备中文字幕")
	sec, err := resolve(ctx, env, req.Secondary, filepath.Join(workDir, "secondary"))
	if err != nil {
		return "", fmt.Errorf("中文字幕: %w", err)
	}

	progress := func(line string) { env.Log("  %s", line) }
	switch req.SyncMode {
	case SyncVideo:
		env.Step(3, total, "按视频音轨对轴")
		env.Log("对轴参考: %s", req.VideoPath)
		primSynced := filepath.Join(workDir, "primary.synced.srt")
		if err := env.Syncer.SyncWithContext(ctx, req.VideoPath, prim, primSynced, progress); err != nil {
			return "", fmt.Errorf("原语字幕对轴失败: %w", err)
		}
		secSynced := filepath.Join(workDir, "secondary.synced.srt")
		if err := env.Syncer.SyncWithContext(ctx, req.VideoPath, sec, secSynced, progress); err != nil {
			return "", fmt.Errorf("中文字幕对轴失败: %w", err)
		}
		prim, sec = primSynced, secSynced
	case SyncNone:
		env.Log("跳过对轴")
	default:
		env.Step(3, total, "以原语字幕为基准对轴")
		secSynced := filepath.Join(workDir, "secondary.synced.srt")
		if err := env.Syncer.SyncWithContext(ctx, prim, sec, secSynced, progress); err != nil {
			return "", fmt.Errorf("对轴失败: %w", err)
		}
		sec = secSynced
	}

	env.Step(total, total, "生成双语 ASS")
	if err := os.MkdirAll(filepath.Dir(req.OutPath), 0755); err != nil {
		return "", err
	}
	if err := ass.GenerateBilingualASS(prim, sec, req.OutPath, env.Style); err != nil {
		return "", err
	}
	env.Log("已写入 %s", req.OutPath)
	return req.OutPath, nil
}

// resolve materializes a source as a UTF-8 SRT file at base+".srt".
func resolve(ctx context.Context, env Env, src Source, base string) (string, error) {
	out := base + ".srt"
	switch src.Kind {
	case KindFile:
		env.Log("读取文件 %s", src.Path)
		if _, err := os.Stat(src.Path); err != nil {
			return "", err
		}
		return normalize(ctx, env, src.Path, out)

	case KindTrack:
		env.Log("从 %s 提取字幕轨 #%d (需要读完整个文件，远程文件会较慢)", src.Path, src.Track)
		if err := env.Tools.ExtractSubtitle(ctx, src.Path, src.Track, out); err != nil {
			return "", err
		}
		return out, nil

	case KindURL:
		if env.Download == nil {
			return "", fmt.Errorf("未配置下载器")
		}
		env.Log("下载 %s", src)
		raw, err := env.Download(src.URL, base+".download")
		if err != nil {
			return "", err
		}
		return normalize(ctx, env, raw, out)

	case KindServer:
		if env.ServerFetch == nil {
			return "", fmt.Errorf("未连接媒体服务器")
		}
		env.Log("从媒体服务器获取字幕轨 #%d", src.StreamIndex)
		data, err := env.ServerFetch(src.ItemID, src.StreamIndex)
		if err != nil {
			return "", err
		}
		raw := base + ".server.srt"
		if err := os.WriteFile(raw, data, 0644); err != nil {
			return "", err
		}
		return normalize(ctx, env, raw, out)
	}
	return "", fmt.Errorf("未知字幕来源类型: %q", src.Kind)
}

// normalize converts non-SRT or non-UTF-8 subtitles via ffmpeg; plain UTF-8 SRT
// files are used as-is.
func normalize(ctx context.Context, env Env, in, out string) (string, error) {
	if strings.EqualFold(filepath.Ext(in), ".srt") && media.DetectLegacyEncoding(in) == "" {
		return in, nil
	}
	env.Log("转换为 UTF-8 SRT")
	if err := env.Tools.ConvertToSRT(ctx, in, out); err != nil {
		return "", err
	}
	return out, nil
}

// OutputName returns "<video base>.<native>-<target>.ass" next to the video.
func OutputName(videoPath, native, target string) string {
	if native == "" {
		native = "zh"
	}
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	if target == "" {
		return base + "." + native + ".bilingual.ass"
	}
	return fmt.Sprintf("%s.%s-%s.ass", base, native, target)
}
