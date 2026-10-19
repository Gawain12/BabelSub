package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gawain12/BabelSub/pkg/adapter"
	"github.com/Gawain12/BabelSub/pkg/config"
	"github.com/Gawain12/BabelSub/pkg/downloader"
	"github.com/Gawain12/BabelSub/pkg/lang"
	"github.com/Gawain12/BabelSub/pkg/media"
	"github.com/Gawain12/BabelSub/pkg/pipeline"
	"github.com/Gawain12/BabelSub/pkg/syncer"
)

func handleCLI() {
	cmd := os.Args[1]
	switch cmd {
	case "config":
		cmdConfig()
	case "scan":
		cmdScan()
	case "fetch":
		cmdFetch()
	case "probe":
		cmdProbe()
	case "merge":
		cmdMerge()
	case "version", "--version", "-v":
		fmt.Println("babelsub", version)
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Printf("未知命令: %s\n\n", cmd)
		printHelp()
		os.Exit(1)
	}
}

func mustLoadServer() (*config.Config, adapter.MediaServerAdapter) {
	cfg, err := config.LoadConfig()
	if err != nil || cfg.Server.Host == "" {
		fmt.Println("[-] 媒体服务器尚未配置。请先运行 `babelsub config` 进行连接配置。")
		os.Exit(1)
	}
	adp, err := newAdapter(cfg)
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		os.Exit(1)
	}
	return cfg, adp
}

func cmdConfig() {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	serverType := fs.String("type", "", "媒体服务类型 (emby / jellyfin / plex)")
	host := fs.String("host", "", "服务器地址 URL (如 http://192.168.1.100:8096)")
	token := fs.String("token", "", "API 密钥或 X-Plex-Token")
	audioLang := fs.String("audio", "", "默认筛选的原声音轨语言 (如 ja / en / fr / ko)")
	nativeLang := fs.String("native", "", "默认辅助字幕语言 (如 zh / en)")
	var mappings multiFlag
	fs.Var(&mappings, "map", "路径映射 服务器路径=本机路径，可重复 (如 --map /Movies=/Volumes/115/Movies)")
	fs.Parse(os.Args[2:])

	cfg, _ := config.LoadConfig()
	if *host != "" {
		cfg.Server.Host = strings.TrimRight(*host, "/")
	}
	if *token != "" {
		cfg.Server.Token = *token
	}
	if *serverType != "" {
		cfg.Server.Type = *serverType
	}
	if *audioLang != "" {
		cfg.Language.TargetAudio = *audioLang
	}
	if *nativeLang != "" {
		cfg.Language.NativeSub = *nativeLang
	}
	if len(mappings) > 0 {
		cfg.PathMappings = nil
		for _, m := range mappings {
			from, to, ok := strings.Cut(m, "=")
			if !ok {
				fmt.Printf("[-] 无效的 --map 参数: %s (应为 服务器路径=本机路径)\n", m)
				os.Exit(1)
			}
			cfg.PathMappings = append(cfg.PathMappings, config.PathMapping{From: from, To: to})
		}
	}

	if cfg.Server.Host == "" || cfg.Server.Token == "" {
		fmt.Println("错误: --host 和 --token 为必填项。")
		fmt.Println("示例: babelsub config --type emby --host http://192.168.1.100:8096 --token your_token")
		os.Exit(1)
	}

	adp, err := newAdapter(cfg)
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[*] 正在测试连接至 %s (%s)...\n", cfg.Server.Type, cfg.Server.Host)
	if err := adp.Ping(); err != nil {
		fmt.Printf("[-] 连接失败: %v\n", err)
		os.Exit(1)
	}
	if err := config.SaveConfig(cfg); err != nil {
		fmt.Printf("[-] 保存失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] 连接成功！配置已保存至: %s\n", config.ConfigFilePath())
}

func cmdScan() {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	langFlag := fs.String("lang", "", "按原声音轨语言过滤 (如 ja 日语, en 英语, fr 法语, ko 韩语；all 表示不过滤；留空使用配置默认值)")
	missingOnly := fs.Bool("missing-only", false, "仅列出尚未制作对应双语字幕的电影条目")
	keyword := fs.String("query", "", "按电影名称或关键词进一步筛选")
	fs.Parse(os.Args[2:])

	cfg, adp := mustLoadServer()
	filterLang := *langFlag
	if filterLang == "" {
		filterLang = cfg.Language.TargetAudio
	}
	if filterLang == "all" {
		filterLang = ""
	}

	fmt.Printf("[*] 正在零扫盘检索媒体库 (音轨语言: %s)...\n", orDash(filterLang))
	movies, err := adp.ListMovies(filterLang, cfg.Language.NativeSub)
	if err != nil {
		fmt.Printf("[-] 检索媒体库失败: %v\n", err)
		os.Exit(1)
	}

	matchedCount := 0
	kw := strings.ToLower(*keyword)
	fmt.Println("--------------------------------------------------------------------------------")
	for _, m := range movies {
		if *missingOnly && m.HasBilingual {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(m.Name), kw) && !strings.Contains(strings.ToLower(m.OriginalTitle), kw) {
			continue
		}

		matchedCount++
		tag := "待制备"
		if m.HasBilingual {
			tag = "已就绪"
		}
		subCount := 0
		for _, s := range m.Streams {
			if s.Type == "Subtitle" {
				subCount++
			}
		}
		matched := ""
		if m.MatchedBy == "country" {
			matched = " (按出品国家推断)"
		}
		fmt.Printf("[%s] %s (年份: %d | ID: %s)\n", tag, m.Name, m.Year, m.ID)
		fmt.Printf("       音轨: %s%s | 现有字幕: %d条 | 路径: %s\n", orDash(m.TargetAudio), matched, subCount, cfg.MapPath(m.Path))
	}
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("[+] 检索完成，共匹配到 %d 部影片。\n", matchedCount)
}

func cmdFetch() {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	query := fs.String("query", "", "电影片名 (例如: 'Swing Girls' 或 '摇摆少女')")
	langFlag := fs.String("lang", "", "仅显示指定语言的字幕 (如 ja, zh, en)")
	save := fs.String("save", "", "将匹配到的字幕下载到该目录")
	fs.Parse(os.Args[2:])

	if *query == "" {
		fmt.Println("用法: babelsub fetch --query <片名> [--lang ja/zh/en] [--save 目录]")
		os.Exit(1)
	}

	cfg, _ := config.LoadConfig()
	p := downloader.NewProviderWithConfig(cfg.Providers)
	fmt.Printf("[*] 正在检索: %s (字幕源: %s)\n", *query, strings.Join(p.Enabled(), ", "))
	sources, errs := p.Search(*query, *langFlag)
	for _, name := range p.Enabled() {
		if e, ok := errs[name]; ok {
			fmt.Printf("[-] %s 检索失败: %v\n", name, e)
		}
	}
	if len(sources) == 0 && len(errs) == len(p.Enabled()) {
		os.Exit(1)
	}

	matched := 0
	counts := map[string]int{}
	fmt.Printf("[+] 找到以下字幕资源:\n")
	for _, src := range sources {
		if *langFlag != "" && !lang.Matches(src.Language, *langFlag) && !strings.EqualFold(src.Language, *langFlag) {
			continue
		}
		matched++
		counts[src.Provider]++
		fmt.Printf("  [%d] %-13s [%s] %s\n", matched, src.Provider, strings.ToUpper(src.Language), src.Filename)
		if src.Info != "" {
			fmt.Printf("       %s\n", src.Info)
		}
		fmt.Printf("       下载链接: %s\n", src.DownloadURL)
		if *save != "" {
			os.MkdirAll(*save, 0755)
			base := strings.TrimSuffix(filepath.Base(src.Filename), filepath.Ext(src.Filename))
			dst := filepath.Join(*save, fmt.Sprintf("%s.%s.%d", base, src.Provider, matched))
			if out, err := p.Fetch(src.DownloadURL, dst); err != nil {
				fmt.Printf("       [-] 下载失败: %v\n", err)
			} else {
				fmt.Printf("       [+] 已保存: %s\n", out)
			}
		}
	}
	if matched == 0 {
		fmt.Println("  (未找到匹配指定语种的字幕条目)")
		return
	}
	var summary []string
	for _, name := range p.Enabled() {
		summary = append(summary, fmt.Sprintf("%s %d", name, counts[name]))
	}
	fmt.Printf("[+] 共 %d 条 (%s)\n", matched, strings.Join(summary, ", "))
}

func cmdProbe() {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println("用法: babelsub probe <视频文件或直链 URL>")
		fmt.Println("列出 MKV / MP4 等容器内的音轨与字幕轨，以及同目录下的外挂字幕。")
	}
	fs.Parse(os.Args[2:])
	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}
	target := fs.Arg(0)
	cfg, _ := config.LoadConfig()
	t := media.NewTools(cfg.FFmpegBin, cfg.FFprobeBin)
	res, err := t.Probe(context.Background(), target)
	if err != nil {
		fmt.Printf("[-] %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] %s  (%s, %s)\n", target, res.Format, fmtDuration(res.DurationSec))
	for _, tr := range res.Tracks {
		if tr.Type == "Video" {
			continue
		}
		flags := []string{}
		if tr.Default {
			flags = append(flags, "默认")
		}
		if tr.Forced {
			flags = append(flags, "强制")
		}
		if tr.Type == "Subtitle" && !tr.TextBase {
			flags = append(flags, "图形字幕,无法直接提取")
		}
		extra := ""
		if len(flags) > 0 {
			extra = " [" + strings.Join(flags, ",") + "]"
		}
		kind := "音轨"
		if tr.Type == "Subtitle" {
			kind = "字幕"
		}
		fmt.Printf("  #%-3d %s  %-4s %-18s %s%s\n", tr.Index, kind, orDash(tr.Language), tr.Codec, tr.Title, extra)
	}
	if !strings.HasPrefix(target, "http") {
		for _, sc := range media.Sidecars(target) {
			fmt.Printf("  外挂  %-4s %s\n", orDash(sc.Language), sc.Name)
		}
	}
	fmt.Println("\n提示: 用 `babelsub merge --media <视频> --ref-track <编号> --in-track <编号>` 直接用内嵌轨道制作双语字幕。")
}

func cmdMerge() {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	refSub := fs.String("ref", "", "【原语字幕】参考基准字幕文件 (.srt/.ass/.vtt)，作为对齐标杆并显示在上方")
	targetSub := fs.String("in", "", "【中文字幕】待对齐的翻译字幕文件 (.srt/.ass/.vtt)，显示在下方")
	mediaPath := fs.String("media", "", "【视频文件】MKV/MP4 等；配合 --ref-track/--in-track 使用内嵌轨道，或用于音轨对轴")
	refTrack := fs.Int("ref-track", -1, "从 --media 中提取的原语字幕轨编号 (见 babelsub probe)")
	inTrack := fs.Int("in-track", -1, "从 --media 中提取的中文字幕轨编号")
	syncMode := fs.String("sync", "", "对轴方式: primary(以原语字幕为准,默认) / video(以视频音轨为准) / none(不对轴)")
	targetLang := fs.String("lang", "", "原语语言代码，用于默认输出文件名 (如 ja)")
	outASS := fs.String("out", "", "【输出路径】双语 .ass 路径；指定 --media 时默认写在视频旁")
	force := fs.Bool("force", false, "覆盖已存在的输出文件")
	fs.Parse(os.Args[2:])

	usage := func() {
		fmt.Println("用法:")
		fmt.Println("  babelsub merge --ref <原语字幕> --in <中文字幕> --out <输出.ass>")
		fmt.Println("  babelsub merge --media movie.mkv --ref-track 3 --in-track 4          # 内嵌轨道，默认不对轴")
		fmt.Println("  babelsub merge --media movie.mkv --ref ja.srt --in zh.srt --sync video  # 以视频音轨对轴")
		fmt.Println("\n说明:")
		fmt.Println("  默认以原语字幕为基准运行 ffsubsync 校准中文字幕，")
		fmt.Println("  并按照标准 1080p 画布输出原语言在上、中文居下的双语 ASS 字幕。")
	}

	primary, ok1 := cliSource(*refSub, *mediaPath, *refTrack)
	secondary, ok2 := cliSource(*targetSub, *mediaPath, *inTrack)
	if !ok1 || !ok2 {
		usage()
		os.Exit(1)
	}

	mode := *syncMode
	if mode == "" {
		mode = pipeline.SyncPrimary
		// Two tracks muxed into the same file are already in sync with each other.
		if primary.Kind == pipeline.KindTrack && secondary.Kind == pipeline.KindTrack {
			mode = pipeline.SyncNone
		}
	}

	cfg, _ := config.LoadConfig()
	out := *outASS
	if out == "" {
		if *mediaPath == "" {
			usage()
			os.Exit(1)
		}
		l := lang.Normalize(*targetLang)
		if l == "" {
			l = lang.Normalize(cfg.Language.TargetAudio)
		}
		out = pipeline.OutputName(*mediaPath, cfg.Language.NativeSub, l)
	}
	if _, err := os.Stat(out); err == nil && !*force {
		fmt.Printf("[-] 输出文件已存在: %s (使用 --force 覆盖)\n", out)
		os.Exit(1)
	}

	p := downloader.NewProviderWithConfig(cfg.Providers)
	env := pipeline.Env{
		Tools:    media.NewTools(cfg.FFmpegBin, cfg.FFprobeBin),
		Syncer:   syncer.NewSyncer(cfg.FFsubsyncBin),
		Style:    &cfg.Styling,
		Download: p.Fetch,
		Log:      func(f string, a ...any) { fmt.Printf("      "+f+"\n", a...) },
		Step:     func(i, n int, t string) { fmt.Printf("[%d/%d] %s...\n", i, n, t) },
	}
	work, _ := os.MkdirTemp("", "babelsub-merge-")
	defer os.RemoveAll(work)

	req := pipeline.Request{Primary: primary, Secondary: secondary, SyncMode: mode, VideoPath: *mediaPath, OutPath: out}
	if _, err := pipeline.Run(context.Background(), env, req, work); err != nil {
		fmt.Printf("[-] 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] 成功输出双语字幕: %s\n", out)
}

func cliSource(file, mediaPath string, track int) (pipeline.Source, bool) {
	switch {
	case file != "":
		if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
			return pipeline.Source{Kind: pipeline.KindURL, URL: file}, true
		}
		return pipeline.Source{Kind: pipeline.KindFile, Path: file}, true
	case track >= 0 && mediaPath != "":
		return pipeline.Source{Kind: pipeline.KindTrack, Path: mediaPath, Track: track}, true
	}
	return pipeline.Source{}, false
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fmtDuration(sec float64) string {
	t := int(sec)
	return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
}

func printHelp() {
	fmt.Printf("BabelSub (巴别字幕) v%s - 专为外语学习打造的媒体库双语字幕系统\n", version)
	fmt.Println("\n用法:")
	fmt.Println("  babelsub <命令> [参数...]")
	fmt.Println("\n常用命令:")
	fmt.Println("  config    配置媒体服务器连接 (Emby / Jellyfin / Plex)")
	fmt.Println("            参数: --type <emby/jellyfin/plex> --host <URL> --token <KEY> [--audio ja] [--native zh]")
	fmt.Println("                  [--map 服务器路径=本机路径]  (可重复，用于把媒体库路径映射到本机挂载点)")
	fmt.Println("")
	fmt.Println("  scan      快速检索媒体库影视条目 (零扫盘，不伤网盘)")
	fmt.Println("            参数: [--lang ja/en/fr/ko/all] [--missing-only] [--query 关键词]")
	fmt.Println("            示例: babelsub scan --lang ja --missing-only  # 查看未就绪双语的日语电影")
	fmt.Println("")
	fmt.Println("  fetch     在线搜索字幕 (SubtitleCat 免 Key；ASSRT / OpenSubtitles 需在设置中填写 token / API key)")
	fmt.Println("            参数: --query <片名> [--lang 语言代码] [--save 下载目录]")
	fmt.Println("            示例: babelsub fetch --query \"Swing Girls\" --lang ja")
	fmt.Println("")
	fmt.Println("  probe     列出 MKV / MP4 等视频内的音轨、字幕轨与同目录外挂字幕 (支持直链 URL)")
	fmt.Println("            示例: babelsub probe ~/Movies/Swing.Girls.2004.mkv")
	fmt.Println("")
	fmt.Println("  merge     对轴并缝合生成双语 ASS 字幕")
	fmt.Println("            参数: --ref <原语字幕> --in <中文字幕> --out <输出.ass>")
	fmt.Println("                  [--media 视频] [--ref-track N] [--in-track N] [--sync primary/video/none] [--force]")
	fmt.Println("            示例: babelsub merge --ref ja.srt --in zh.srt --out movie.zh-ja.ass")
	fmt.Println("                  babelsub merge --media movie.mkv --ref-track 3 --in zh.srt")
	fmt.Println("")
	fmt.Println("  (无参数)  启动本地 Web 控制台服务")
	fmt.Println("            参数: --port 18980 (默认端口)  --listen 0.0.0.0 (仅本机可设为 127.0.0.1)")
	fmt.Println("            访问: http://localhost:18980")
	fmt.Println("\n跨平台说明:")
	fmt.Println("  已原生支持 macOS (Apple Silicon / Intel)、Linux (x86_64 / arm64) 以及 Windows (x86_64)。")
}
