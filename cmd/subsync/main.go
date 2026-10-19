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

	"github.com/gawaintan/subsync-studio/pkg/adapter"
	"github.com/gawaintan/subsync-studio/pkg/ass"
	"github.com/gawaintan/subsync-studio/pkg/config"
	"github.com/gawaintan/subsync-studio/pkg/downloader"
	"github.com/gawaintan/subsync-studio/pkg/syncer"
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
	fmt.Printf("===================================================\n")
	fmt.Printf(" [SubSync Studio] GUI & REST Core running!\n")
	fmt.Printf(" Web UI Address: http://localhost:%d\n", *port)
	fmt.Printf(" Config location: %s\n", config.ConfigFilePath())
	fmt.Printf("===================================================\n")

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
		serverType := cmdConfig.String("type", "emby", "Server type (emby/jellyfin/plex)")
		host := cmdConfig.String("host", "", "Server address URL")
		token := cmdConfig.String("token", "", "API key or token")
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

		if cfg.Server.Host == "" || cfg.Server.Token == "" {
			fmt.Println("Error: --host and --token are required")
			os.Exit(1)
		}

		var adp adapter.MediaServerAdapter
		if cfg.Server.Type == "plex" {
			adp = adapter.NewPlexAdapter(cfg.Server.Host, cfg.Server.Token)
		} else {
			adp = adapter.NewEmbyAdapter(cfg.Server.Host, cfg.Server.Token, cfg.Server.Type == "jellyfin")
		}

		fmt.Printf("[*] Testing connection to %s at %s...\n", cfg.Server.Type, cfg.Server.Host)
		if err := adp.Ping(); err != nil {
			fmt.Printf("[-] Connection failed: %v\n", err)
			os.Exit(1)
		}
		config.SaveConfig(cfg)
		fmt.Printf("[+] Successfully connected! Saved configuration to: %s\n", config.ConfigFilePath())

	case "scan":
		cmdScan := flag.NewFlagSet("scan", flag.ExitOnError)
		lang := cmdScan.String("lang", "ja", "Target audio language")
		missingOnly := cmdScan.Bool("missing-only", false, "Filter to items missing bilingual subtitle")
		cmdScan.Parse(os.Args[2:])

		cfg, err := config.LoadConfig()
		if err != nil || cfg.Server.Host == "" {
			fmt.Println("[-] Server not configured. Run `subsync config` first.")
			os.Exit(1)
		}

		var adp adapter.MediaServerAdapter
		if cfg.Server.Type == "plex" {
			adp = adapter.NewPlexAdapter(cfg.Server.Host, cfg.Server.Token)
		} else {
			adp = adapter.NewEmbyAdapter(cfg.Server.Host, cfg.Server.Token, cfg.Server.Type == "jellyfin")
		}

		fmt.Printf("[*] Querying media server for movies with '%s' audio...\n", *lang)
		movies, err := adp.ListMovies(*lang)
		if err != nil {
			fmt.Printf("[-] Failed to query library: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("[+] Found %d movies:\n", len(movies))
		for i, m := range movies {
			if *missingOnly && m.HasBilingual {
				continue
			}
			tag := "[缺失双语]"
			if m.HasBilingual {
				tag = "[已就绪双语]"
			}
			subCount := 0
			for _, s := range m.Streams {
				if s.Type == "Subtitle" {
					subCount++
				}
			}
			fmt.Printf("  [%d] %s %s (ID: %s, 年份: %d)\n", i+1, tag, m.Name, m.ID, m.Year)
			fmt.Printf("       路径: %s\n", m.Path)
			fmt.Printf("       音轨: %s | 字幕轨: %d条\n", m.TargetAudio, subCount)
		}

	case "merge":
		cmdMerge := flag.NewFlagSet("merge", flag.ExitOnError)
		refSub := cmdMerge.String("ref", "", "Reference subtitle for timing (e.g. Japanese .srt)")
		targetSub := cmdMerge.String("in", "", "Target subtitle to synchronize (e.g. Chinese .srt)")
		outASS := cmdMerge.String("out", "", "Output bilingual .ass path")
		cmdMerge.Parse(os.Args[2:])

		if *refSub == "" || *targetSub == "" || *outASS == "" {
			fmt.Println("Usage: subsync merge --ref <ref.srt> --in <target.srt> --out <output.ass>")
			os.Exit(1)
		}

		cfg, _ := config.LoadConfig()
		s := syncer.NewSyncer(cfg.FFsubsyncBin)
		syncedSRT := filepath.Join(os.TempDir(), "synced_temp.srt")

		fmt.Println("[*] Running ffsubsync timing alignment...")
		if err := s.Sync(*refSub, *targetSub, syncedSRT); err != nil {
			fmt.Printf("[-] Subtitle sync failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[+] Timing synced! Generating bilingual ASS...")

		if err := ass.GenerateBilingualASS(*refSub, syncedSRT, *outASS, &cfg.Styling); err != nil {
			fmt.Printf("[-] ASS generation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[+] Bilingual ASS saved successfully: %s\n", *outASS)

	case "fetch":
		cmdFetch := flag.NewFlagSet("fetch", flag.ExitOnError)
		query := cmdFetch.String("query", "", "Movie title to search")
		cmdFetch.Parse(os.Args[2:])

		if *query == "" {
			fmt.Println("Usage: subsync fetch --query <MovieName>")
			os.Exit(1)
		}

		p := downloader.NewProvider()
		fmt.Printf("[*] Searching subtitle sources for: %s...\n", *query)
		sources, err := p.FetchSubtitleCat(*query)
		if err != nil {
			fmt.Printf("[-] Search failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[+] Found %d available subtitle tracks:\n", len(sources))
		for idx, src := range sources {
			fmt.Printf("  [%d] [%s] %s\n", idx+1, src.Language, src.Filename)
		}

	default:
		printHelp()
	}
}

func printHelp() {
	fmt.Println("SubSync Studio - High Performance Bilingual Subtitle Pipeline")
	fmt.Println("\nUsage:")
	fmt.Println("  subsync                                   Start Web GUI and REST server")
	fmt.Println("  subsync config --type emby --host <url> --token <token>")
	fmt.Println("  subsync scan [--lang ja] [--missing-only]")
	fmt.Println("  subsync fetch --query <MovieTitle>")
	fmt.Println("  subsync merge --ref <ref.srt> --in <target.srt> --out <output.ass>")
}
