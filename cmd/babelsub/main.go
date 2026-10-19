package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gawain12/BabelSub/pkg/adapter"
	"github.com/Gawain12/BabelSub/pkg/ass"
	"github.com/Gawain12/BabelSub/pkg/config"
	"github.com/Gawain12/BabelSub/pkg/downloader"
	"github.com/Gawain12/BabelSub/pkg/syncer"
)

//go:embed web/index.html
var indexHTML []byte

type Server struct {
	cfg *config.Config
}

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		handleCLI()
		return
	}

	// Server mode
	port := flag.Int("port", 18980, "Web/API server port")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Warning: failed to load config: %v", err)
	}

	s := &Server{cfg: cfg}

	http.HandleFunc("/api/config", s.handleConfig)
	http.HandleFunc("/api/movies", s.handleMovies)
	http.HandleFunc("/api/refresh", s.handleRefresh)
	http.HandleFunc("/", s.handleUI)

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	fmt.Printf("====================================================================\n")
	fmt.Printf(" BabelSub (巴别字幕) - 媒体库外语双语字幕工具\n")
	fmt.Printf(" 控制台地址: http://localhost:%d\n", *port)
	fmt.Printf(" 配置文件: %s\n", config.ConfigFilePath())
	fmt.Printf("====================================================================\n")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func (s *Server) getAdapter() (adapter.MediaServerAdapter, error) {
	switch strings.ToLower(s.cfg.Server.Type) {
	case "emby":
		return adapter.NewEmbyAdapter(s.cfg.Server.Host, s.cfg.Server.Token, false), nil
	case "jellyfin":
		return adapter.NewEmbyAdapter(s.cfg.Server.Host, s.cfg.Server.Token, true), nil
	case "plex":
		return adapter.NewPlexAdapter(s.cfg.Server.Host, s.cfg.Server.Token), nil
	default:
		return nil, fmt.Errorf("unsupported server type: %s", s.cfg.Server.Type)
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		json.NewEncoder(w).Encode(s.cfg)
		return
	}

	if r.Method == "POST" {
		var newCfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		s.cfg = &newCfg
		adp, err := s.getAdapter()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := adp.Ping(); err != nil {
			http.Error(w, fmt.Sprintf("Connection test failed: %v", err), http.StatusBadRequest)
			return
		}

		if err := config.SaveConfig(s.cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "message": "Saved successfully!"})
		return
	}
}

func (s *Server) handleMovies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = s.cfg.Language.TargetAudio
	}

	adp, err := s.getAdapter()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	movies, err := adp.ListMovies(lang)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list movies: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":  len(movies),
		"movies": movies,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id param", http.StatusBadRequest)
		return
	}

	adp, err := s.getAdapter()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := adp.RefreshItem(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "id": id})
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func handleCLI() {
	cmd := os.Args[1]
	switch cmd {
	case "config":
		cmdConfig := flag.NewFlagSet("config", flag.ExitOnError)
		serverType := cmdConfig.String("type", "emby", "媒体服务类型 (emby / jellyfin / plex)")
		host := cmdConfig.String("host", "", "服务器地址 URL (如 http://192.168.1.100:8096)")
		token := cmdConfig.String("token", "", "API 密钥或 X-Plex-Token")
		audioLang := cmdConfig.String("audio", "ja", "默认筛选的原声音轨语言 (如 ja / en / fr / ko)")
		nativeLang := cmdConfig.String("native", "zh", "默认辅助字幕语言 (如 zh / en)")
		cmdConfig.Parse(os.Args[2:])

		cfg, _ := config.LoadConfig()
		if *host != "" {
			cfg.Server.Host = *host
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

		if cfg.Server.Host == "" || cfg.Server.Token == "" {
			fmt.Println("错误: --host 和 --token 为必填项。")
			fmt.Println("示例: babelsub config --type emby --host http://192.168.1.100:8096 --token your_token")
			os.Exit(1)
		}

		var adp adapter.MediaServerAdapter
		if cfg.Server.Type == "plex" {
			adp = adapter.NewPlexAdapter(cfg.Server.Host, cfg.Server.Token)
		} else {
			adp = adapter.NewEmbyAdapter(cfg.Server.Host, cfg.Server.Token, cfg.Server.Type == "jellyfin")
		}

		fmt.Printf("[*] 正在测试连接至 %s (%s)...\n", cfg.Server.Type, cfg.Server.Host)
		if err := adp.Ping(); err != nil {
			fmt.Printf("[-] 连接失败: %v\n", err)
			os.Exit(1)
		}
		config.SaveConfig(cfg)
		fmt.Printf("[+] 连接成功！配置已保存至: %s\n", config.ConfigFilePath())

	case "scan":
		cmdScan := flag.NewFlagSet("scan", flag.ExitOnError)
		lang := cmdScan.String("lang", "", "按原声音轨语言过滤 (如 ja 日语, en 英语, fr 法语, ko 韩语，留空则使用配置默认值)")
		missingOnly := cmdScan.Bool("missing-only", false, "仅列出尚未制作对应双语字幕的电影条目")
		keyword := cmdScan.String("query", "", "按电影名称或关键词进一步筛选")
		cmdScan.Parse(os.Args[2:])

		cfg, err := config.LoadConfig()
		if err != nil || cfg.Server.Host == "" {
			fmt.Println("[-] 媒体服务器尚未配置。请先运行 `babelsub config` 进行连接配置。")
			os.Exit(1)
		}

		filterLang := *lang
		if filterLang == "" {
			filterLang = cfg.Language.TargetAudio
		}

		var adp adapter.MediaServerAdapter
		if cfg.Server.Type == "plex" {
			adp = adapter.NewPlexAdapter(cfg.Server.Host, cfg.Server.Token)
		} else {
			adp = adapter.NewEmbyAdapter(cfg.Server.Host, cfg.Server.Token, cfg.Server.Type == "jellyfin")
		}

		fmt.Printf("[*] 正在零扫盘检索媒体库 (音轨语言: %s)...\n", filterLang)
		movies, err := adp.ListMovies(filterLang)
		if err != nil {
			fmt.Printf("[-] 检索媒体库失败: %v\n", err)
			os.Exit(1)
		}

		matchedCount := 0
		fmt.Println("--------------------------------------------------------------------------------")
		for _, m := range movies {
			if *missingOnly && m.HasBilingual {
				continue
			}
			if *keyword != "" && !strings.Contains(strings.ToLower(m.Name), strings.ToLower(*keyword)) &&
				!strings.Contains(strings.ToLower(m.OriginalTitle), strings.ToLower(*keyword)) {
				continue
			}

			matchedCount++
			tag := "[待制备]"
			if m.HasBilingual {
				tag = "[已就绪]"
			}
			subCount := 0
			for _, s := range m.Streams {
				if s.Type == "Subtitle" {
					subCount++
				}
			}
			fmt.Printf("[%s] %s (年份: %d | ID: %s)\n", tag, m.Name, m.Year, m.ID)
			fmt.Printf("       音轨: %s | 现有字幕: %d条 | 路径: %s\n", m.TargetAudio, subCount, m.Path)
		}
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Printf("[+] 检索完成，共匹配到 %d 部影片。\n", matchedCount)

	case "fetch":
		cmdFetch := flag.NewFlagSet("fetch", flag.ExitOnError)
		query := cmdFetch.String("query", "", "电影片名 (例如: 'Swing Girls' 或 '摇摆少女')")
		lang := cmdFetch.String("lang", "", "仅显示指定语言的字幕 (如 ja, zh, en)")
		cmdFetch.Parse(os.Args[2:])

		if *query == "" {
			fmt.Println("用法: babelsub fetch --query <片名> [--lang ja/zh/en]")
			os.Exit(1)
		}

		p := downloader.NewProvider()
		fmt.Printf("[*] 正在公开字幕源检索: %s...\n", *query)
		sources, err := p.FetchSubtitleCat(*query)
		if err != nil {
			fmt.Printf("[-] 检索失败: %v\n", err)
			os.Exit(1)
		}

		matched := 0
		fmt.Printf("[+] 找到以下字幕资源:\n")
		for _, src := range sources {
			if *lang != "" && !strings.EqualFold(src.Language, *lang) {
				continue
			}
			matched++
			fmt.Printf("  [%d] [%s] %s\n       下载链接: %s\n", matched, strings.ToUpper(src.Language), src.Filename, src.DownloadURL)
		}
		if matched == 0 {
			fmt.Println("  (未找到匹配指定语种的字幕条目)")
		}

	case "merge":
		cmdMerge := flag.NewFlagSet("merge", flag.ExitOnError)
		refSub := cmdMerge.String("ref", "", "【参考基准字幕】：通常为原片语言字幕（如日语/英语 .srt），其时间轴作为对齐标杆")
		targetSub := cmdMerge.String("in", "", "【待对齐字幕】：通常为中文翻译字幕（.srt），将按参考轴自动修正时间戳偏差与拉伸")
		outASS := cmdMerge.String("out", "", "【输出路径】：合成后的双语 .ass 字幕文件路径")
		cmdMerge.Parse(os.Args[2:])

		if *refSub == "" || *targetSub == "" || *outASS == "" {
			fmt.Println("用法:")
			fmt.Println("  babelsub merge --ref <原语言参考轴.srt> --in <待对齐翻译字幕.srt> --out <输出双语.ass>")
			fmt.Println("\n说明:")
			fmt.Println("  工具会自动运行 ffsubsync 将 --in 字幕与 --ref 字幕进行时间轴对齐，")
			fmt.Println("  并按照标准 1080p 画布输出原语言在上、中文居下的双语 ASS 字幕。")
			os.Exit(1)
		}

		cfg, _ := config.LoadConfig()
		s := syncer.NewSyncer(cfg.FFsubsyncBin)
		syncedSRT := filepath.Join(os.TempDir(), "synced_temp.srt")

		fmt.Println("[1/2] 正在运行 ffsubsync 进行时间轴自适应校准...")
		if err := s.Sync(*refSub, *targetSub, syncedSRT); err != nil {
			fmt.Printf("[-] 对轴失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[2/2] 校准完毕，正在生成标准排版双语 ASS...")

		if err := ass.GenerateBilingualASS(*refSub, syncedSRT, *outASS, &cfg.Styling); err != nil {
			fmt.Printf("[-] ASS 合成失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[+] 成功输出双语字幕: %s\n", *outASS)

	case "help", "--help", "-h":
		printHelp()

	default:
		fmt.Printf("未知命令: %s\n\n", cmd)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println("BabelSub (巴别字幕) - 专为外语学习打造的媒体库双语字幕系统")
	fmt.Println("\n用法:")
	fmt.Println("  babelsub <命令> [参数...]")
	fmt.Println("\n常用命令:")
	fmt.Println("  config    配置媒体服务器连接 (Emby / Jellyfin / Plex)")
	fmt.Println("            参数: --type <emby/jellyfin/plex> --host <URL> --token <KEY> [--audio ja] [--native zh]")
	fmt.Println("")
	fmt.Println("  scan      快速检索媒体库影视条目 (零扫盘，不伤网盘)")
	fmt.Println("            参数: [--lang ja/en/fr/ko] [--missing-only] [--query 关键词]")
	fmt.Println("            示例: babelsub scan --lang ja --missing-only  # 查看未就绪双语的日语电影")
	fmt.Println("")
	fmt.Println("  fetch     在线搜索指定电影的公开字幕源 (免 Key)")
	fmt.Println("            参数: --query <片名> [--lang 语言代码]")
	fmt.Println("            示例: babelsub fetch --query \"Swing Girls\" --lang ja")
	fmt.Println("")
	fmt.Println("  merge     以原语字幕为基准自动对轴，并缝合生成双语 ASS 字幕")
	fmt.Println("            参数: --ref <参考字幕.srt> --in <待对齐翻译.srt> --out <输出.ass>")
	fmt.Println("            示例: babelsub merge --ref ja.srt --in zh.srt --out movie.zh-ja.ass")
	fmt.Println("")
	fmt.Println("  (无参数)  启动本地 Web 控制台服务")
	fmt.Println("            参数: --port 18980 (默认端口 18980)")
	fmt.Println("            访问: http://localhost:18980")
	fmt.Println("\n跨平台说明:")
	fmt.Println("  已原生支持 macOS (Apple Silicon / Intel)、Linux (x86_64 / arm64) 以及 Windows (x86_64)。")
}
