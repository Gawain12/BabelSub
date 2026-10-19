package main

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Gawain12/BabelSub/pkg/adapter"
	"github.com/Gawain12/BabelSub/pkg/config"
	"github.com/Gawain12/BabelSub/pkg/downloader"
	"github.com/Gawain12/BabelSub/pkg/lang"
	"github.com/Gawain12/BabelSub/pkg/media"
	"github.com/Gawain12/BabelSub/pkg/pipeline"
	"github.com/Gawain12/BabelSub/pkg/syncer"
)

type libCache struct {
	movies   []adapter.MediaItem
	cachedAt time.Time
}

type Server struct {
	mu      sync.RWMutex
	cfg     *config.Config
	cache   map[string]*libCache // key: target|native
	items   map[string]adapter.MediaItem
	fetchMu sync.Mutex
	jobs    *pipeline.JobManager
	workDir string
}

func newServer(cfg *config.Config) *Server {
	work := filepath.Join(os.TempDir(), "babelsub")
	os.MkdirAll(filepath.Join(work, "uploads"), 0755)
	os.MkdirAll(filepath.Join(work, "outputs"), 0755)
	s := &Server{
		cfg:     cfg,
		cache:   map[string]*libCache{},
		items:   map[string]adapter.MediaItem{},
		jobs:    pipeline.NewJobManager(filepath.Join(work, "jobs")),
		workDir: work,
	}
	s.jobs.Start(s.pipelineEnv, s.afterJob)
	return s
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleSaveConfig)
	mux.HandleFunc("GET /api/movies", s.handleMovies)
	mux.HandleFunc("GET /api/movies/{id}", s.handleMovieDetail)
	mux.HandleFunc("GET /api/movies/{id}/poster", s.handlePoster)
	mux.HandleFunc("POST /api/movies/{id}/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/fetch", s.handleFetch)
	mux.HandleFunc("GET /api/fetch/download", s.handleFetchDownload)
	mux.HandleFunc("GET /api/fs", s.handleFS)
	mux.HandleFunc("GET /api/probe", s.handleProbe)
	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("GET /api/jobs", s.handleJobs)
	mux.HandleFunc("POST /api/jobs", s.handleCreateJob)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleCancelJob)
	mux.HandleFunc("GET /api/jobs/{id}/download", s.handleJobDownload)
	mux.HandleFunc("GET /{$}", s.handleUI)
	return mux
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, args...)})
}

func (s *Server) config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Server) adapter() (adapter.MediaServerAdapter, error) {
	cfg := s.config()
	if cfg.Server.Host == "" {
		return nil, errors.New("尚未配置媒体服务器")
	}
	return newAdapter(cfg)
}

func newAdapter(cfg *config.Config) (adapter.MediaServerAdapter, error) {
	switch strings.ToLower(cfg.Server.Type) {
	case "emby":
		return adapter.NewEmbyAdapter(cfg.Server.Host, cfg.Server.Token, false), nil
	case "jellyfin":
		return adapter.NewEmbyAdapter(cfg.Server.Host, cfg.Server.Token, true), nil
	case "plex":
		return adapter.NewPlexAdapter(cfg.Server.Host, cfg.Server.Token), nil
	default:
		return nil, fmt.Errorf("unsupported server type: %s", cfg.Server.Type)
	}
}

func (s *Server) tools() *media.Tools {
	cfg := s.config()
	return media.NewTools(cfg.FFmpegBin, cfg.FFprobeBin)
}

func (s *Server) lookupItem(id string) (adapter.MediaItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	return it, ok
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func toolStatus(p string) map[string]any {
	resolved, err := exec.LookPath(p)
	if err != nil {
		return map[string]any{"ok": false, "path": p}
	}
	return map[string]any{"ok": true, "path": resolved}
}

// ---------- status & config ----------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	t := s.tools()
	home, _ := os.UserHomeDir()
	writeJSON(w, 200, map[string]any{
		"version":           version,
		"os":                runtime.GOOS,
		"config_path":       config.ConfigFilePath(),
		"server_configured": cfg.Server.Host != "",
		"server_type":       cfg.Server.Type,
		"home":              home,
		"tools": map[string]any{
			"ffmpeg":    toolStatus(t.FFmpeg),
			"ffprobe":   toolStatus(t.FFprobe),
			"ffsubsync": toolStatus(syncer.NewSyncer(cfg.FFsubsyncBin).FFsubsyncPath),
		},
	})
}

type configView struct {
	*config.Config
	TokenSet            bool `json:"token_set"`
	ASSRTTokenSet       bool `json:"assrt_token_set"`
	OpenSubtitlesKeySet bool `json:"opensubtitles_key_set"`
}

// clearSecret is sent by the UI to remove a stored provider key (an empty
// value means "keep the saved one").
const clearSecret = "-"

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := *s.config()
	v := configView{
		Config:              &cfg,
		TokenSet:            cfg.Server.Token != "",
		ASSRTTokenSet:       cfg.Providers.ASSRTToken != "",
		OpenSubtitlesKeySet: cfg.Providers.OpenSubtitlesKey != "",
	}
	// never send secrets back to the browser
	cfg.Server.Token = ""
	cfg.Providers.ASSRTToken = ""
	cfg.Providers.OpenSubtitlesKey = ""
	writeJSON(w, 200, v)
}

func keepSecret(next, cur string) string {
	switch strings.TrimSpace(next) {
	case "":
		return cur
	case clearSecret:
		return ""
	}
	return strings.TrimSpace(next)
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	cur := s.config()
	next := *cur
	next.PathMappings = nil
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeErr(w, 400, "无效的配置: %v", err)
		return
	}
	if next.Server.Token == "" {
		next.Server.Token = cur.Server.Token
	}
	next.Providers.ASSRTToken = keepSecret(next.Providers.ASSRTToken, cur.Providers.ASSRTToken)
	next.Providers.OpenSubtitlesKey = keepSecret(next.Providers.OpenSubtitlesKey, cur.Providers.OpenSubtitlesKey)
	next.Server.Host = strings.TrimRight(strings.TrimSpace(next.Server.Host), "/")
	var mappings []config.PathMapping
	for _, m := range next.PathMappings {
		if strings.TrimSpace(m.From) != "" && strings.TrimSpace(m.To) != "" {
			mappings = append(mappings, config.PathMapping{From: strings.TrimSpace(m.From), To: strings.TrimSpace(m.To)})
		}
	}
	next.PathMappings = mappings
	if next.Styling.PlayResX <= 0 || next.Styling.PlayResY <= 0 {
		next.Styling.PlayResX, next.Styling.PlayResY = 1920, 1080
	}

	serverChanged := next.Server != cur.Server
	if serverChanged && next.Server.Host != "" && r.URL.Query().Get("force") != "1" {
		adp, err := newAdapter(&next)
		if err != nil {
			writeErr(w, 400, "%v", err)
			return
		}
		if err := adp.Ping(); err != nil {
			writeJSON(w, 400, map[string]any{"error": fmt.Sprintf("连接测试失败: %v", err), "can_force": true})
			return
		}
	}
	if err := config.SaveConfig(&next); err != nil {
		writeErr(w, 500, "保存失败: %v", err)
		return
	}
	s.mu.Lock()
	s.cfg = &next
	if serverChanged || next.Language != cur.Language {
		s.cache = map[string]*libCache{}
	}
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

// ---------- library ----------

func (s *Server) handleMovies(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	target := r.URL.Query().Get("lang")
	if !r.URL.Query().Has("lang") {
		target = cfg.Language.TargetAudio
	}
	native := cfg.Language.NativeSub
	key := target + "|" + native

	adp, err := s.adapter()
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}

	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()

	s.mu.RLock()
	c := s.cache[key]
	s.mu.RUnlock()
	refresh := r.URL.Query().Get("refresh") == "1"
	if c == nil && !refresh {
		// A previous run's list makes the first page load instant; the UI
		// refreshes it in the background when it is old.
		c = s.loadDiskCache(key)
	}
	if c == nil || refresh {
		movies, err := adp.ListMovies(target, native)
		if err != nil {
			writeErr(w, 502, "读取媒体库失败: %v", err)
			return
		}
		c = &libCache{movies: movies, cachedAt: time.Now()}
		s.saveDiskCache(key, c)
	}
	s.mu.Lock()
	s.cache[key] = c
	for _, m := range c.movies {
		if old, ok := s.items[m.ID]; ok && old.HasBilingual {
			m.HasBilingual = true
		}
		s.items[m.ID] = m
	}
	s.mu.Unlock()

	s.mu.RLock()
	// Re-read bilingual flags: jobs may have produced files since the list was cached.
	out := make([]adapter.MediaItem, len(c.movies))
	for i, m := range c.movies {
		if it, ok := s.items[m.ID]; ok {
			m.HasBilingual = m.HasBilingual || it.HasBilingual
		}
		out[i] = m
	}
	s.mu.RUnlock()

	writeJSON(w, 200, map[string]any{
		"total":     len(out),
		"movies":    out,
		"cached_at": c.cachedAt,
		"lang":      target,
	})
}

type localInfo struct {
	LocalPath    string          `json:"local_path"`
	LocalExists  bool            `json:"local_exists"`
	IsStrm       bool            `json:"is_strm"`
	VideoSource  string          `json:"video_source"`  // best location for ffprobe / audio sync
	VideoRemote  bool            `json:"video_remote"`  // VideoSource is an HTTP URL
	OutputPath   string          `json:"output_path"`   // suggested bilingual path beside the video
	OutputExists bool            `json:"output_exists"` // a file already sits at OutputPath
	CanWrite     bool            `json:"can_write"`     // the video directory is reachable
	Sidecars     []media.Sidecar `json:"sidecars"`
	Mapped       bool            `json:"mapped"`
}

func (s *Server) resolveLocal(it adapter.MediaItem) localInfo {
	cfg := s.config()
	li := localInfo{LocalPath: cfg.MapPath(it.Path)}
	li.Mapped = li.LocalPath != it.Path
	li.IsStrm = strings.EqualFold(filepath.Ext(it.Path), ".strm")
	li.LocalExists = fileExists(li.LocalPath)
	if li.LocalExists {
		li.CanWrite = true
		li.Sidecars = media.Sidecars(li.LocalPath)
		if li.IsStrm {
			if b, err := os.ReadFile(li.LocalPath); err == nil {
				li.VideoSource = strings.TrimSpace(string(b))
			}
		} else {
			li.VideoSource = li.LocalPath
		}
	} else if dir := filepath.Dir(li.LocalPath); li.LocalPath != "" && fileExists(dir) {
		li.CanWrite = true
	}
	if li.VideoSource == "" {
		li.VideoSource = it.MediaURL
	}
	li.VideoRemote = strings.HasPrefix(li.VideoSource, "http://") || strings.HasPrefix(li.VideoSource, "https://")
	if li.LocalPath != "" {
		li.OutputPath = pipeline.OutputName(li.LocalPath, cfg.Language.NativeSub, lang.Normalize(it.TargetAudio))
		li.OutputExists = fileExists(li.OutputPath)
	}
	return li
}

func (s *Server) handleMovieDetail(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookupItem(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "条目不在缓存中，请先刷新媒体库")
		return
	}
	writeJSON(w, 200, map[string]any{"item": it, "local": s.resolveLocal(it)})
}

func (s *Server) handlePoster(w http.ResponseWriter, r *http.Request) {
	adp, err := s.adapter()
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	resp, err := adp.FetchPoster(r.PathValue("id"), 300)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		http.Error(w, "no poster", resp.StatusCode)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	io.Copy(w, resp.Body)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	adp, err := s.adapter()
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	if err := adp.RefreshItem(r.PathValue("id")); err != nil {
		writeErr(w, 502, "刷新失败: %v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (s *Server) handleFetch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		writeErr(w, 400, "缺少搜索词")
		return
	}
	l := r.URL.Query().Get("lang")
	p := downloader.NewProviderWithConfig(s.config().Providers)
	sources, errs := p.Search(query, l)
	if l != "" {
		filtered := sources[:0]
		for _, src := range sources {
			if lang.Matches(src.Language, l) {
				filtered = append(filtered, src)
			}
		}
		sources = filtered
	}
	if sources == nil {
		sources = []downloader.SubtitleSource{}
	}
	res := map[string]any{"query": query, "sources": sources, "providers": p.Enabled(), "errors": errs}
	if len(sources) == 0 && len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e)
		}
		sort.Strings(msgs)
		res["error"] = strings.Join(msgs, "; ")
	}
	writeJSON(w, 200, res)
}

// handleFetchDownload resolves a search result (including assrt:// and
// opensubtitles:// links) and streams the subtitle file to the browser.
func (s *Server) handleFetchDownload(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		writeErr(w, 400, "缺少 url")
		return
	}
	dir, err := os.MkdirTemp(s.workDir, "fetch-")
	if err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	defer os.RemoveAll(dir)
	p := downloader.NewProviderWithConfig(s.config().Providers)
	out, err := p.Fetch(u, filepath.Join(dir, "subtitle"))
	if err != nil {
		writeErr(w, 502, "下载失败: %v", err)
		return
	}
	name := sanitize(strings.TrimSpace(r.URL.Query().Get("name")))
	switch strings.ToLower(filepath.Ext(name)) {
	case ".srt", ".ass", ".ssa", ".vtt", ".zip":
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	name += filepath.Ext(out)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeFile(w, r, out)
}

// ---------- local files ----------

func (s *Server) handleFS(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		writeJSON(w, 200, map[string]any{"path": "", "roots": s.fsRoots()})
		return
	}
	if strings.HasPrefix(p, "~") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[1:])
	}
	p = filepath.Clean(p)
	info, err := os.Stat(p)
	if err != nil {
		writeErr(w, 404, "路径不存在: %s", p)
		return
	}
	if !info.IsDir() {
		writeJSON(w, 200, map[string]any{
			"path":     p,
			"is_file":  true,
			"parent":   filepath.Dir(p),
			"sidecars": media.Sidecars(p),
		})
		return
	}
	entries, err := media.ListDir(p)
	if err != nil {
		writeErr(w, 500, "无法读取目录: %v", err)
		return
	}
	if entries == nil {
		entries = []media.Entry{}
	}
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	writeJSON(w, 200, map[string]any{"path": p, "parent": parent, "entries": entries})
}

func (s *Server) fsRoots() []map[string]string {
	var roots []map[string]string
	add := func(name, p string) {
		if fileExists(p) {
			roots = append(roots, map[string]string{"name": name, "path": p})
		}
	}
	home, _ := os.UserHomeDir()
	add("主目录", home)
	add("影片", filepath.Join(home, "Movies"))
	add("下载", filepath.Join(home, "Downloads"))
	for _, m := range s.config().PathMappings {
		add("映射: "+m.From, m.To)
	}
	switch runtime.GOOS {
	case "darwin":
		if des, err := os.ReadDir("/Volumes"); err == nil {
			for _, de := range des {
				add("卷: "+de.Name(), filepath.Join("/Volumes", de.Name()))
			}
		}
	case "linux":
		for _, d := range []string{"/mnt", "/media"} {
			if des, err := os.ReadDir(d); err == nil {
				for _, de := range des {
					add(de.Name(), filepath.Join(d, de.Name()))
				}
			}
		}
	case "windows":
		for c := 'C'; c <= 'Z'; c++ {
			add(string(c)+":", string(c)+`:\`)
		}
	}
	return roots
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if id := r.URL.Query().Get("item_id"); id != "" {
		it, ok := s.lookupItem(id)
		if !ok {
			writeErr(w, 404, "条目不在缓存中")
			return
		}
		p = s.resolveLocal(it).VideoSource
	}
	if p == "" {
		writeErr(w, 400, "没有可探测的媒体地址（未配置路径映射且服务器未提供直链）")
		return
	}
	res, err := s.tools().Probe(r.Context(), p)
	if err != nil {
		writeErr(w, 502, "%v", err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "上传失败: %v", err)
		return
	}
	defer f.Close()
	name := filepath.Base(hdr.Filename)
	if !media.IsSubtitle(name) {
		writeErr(w, 400, "只支持 .srt / .ass / .ssa / .vtt 字幕文件")
		return
	}
	dir := filepath.Join(s.workDir, "uploads", fmt.Sprint(time.Now().UnixNano()))
	os.MkdirAll(dir, 0755)
	dst := filepath.Join(dir, name)
	out, err := os.Create(dst)
	if err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, f); err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"path": dst, "name": name, "language": media.GuessLangFromName(name)})
}

// ---------- jobs ----------

type createJobReq struct {
	Title     string          `json:"title"`
	ItemID    string          `json:"item_id"`
	Lang      string          `json:"lang"`
	Primary   pipeline.Source `json:"primary"`
	Secondary pipeline.Source `json:"secondary"`
	SyncMode  string          `json:"sync_mode"`
	VideoPath string          `json:"video_path"`
	Output    struct {
		Mode string `json:"mode"` // beside | custom | download
		Path string `json:"path"`
	} `json:"output"`
	Overwrite bool `json:"overwrite"`
	Refresh   bool `json:"refresh"`
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var in createJobReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "无效请求: %v", err)
		return
	}
	req := pipeline.Request{Primary: in.Primary, Secondary: in.Secondary, SyncMode: in.SyncMode, VideoPath: in.VideoPath}

	switch in.Output.Mode {
	case "download":
		req.OutPath = filepath.Join(s.workDir, "outputs", fmt.Sprintf("%d", time.Now().UnixNano()), sanitize(in.Title)+suffix(s.config().Language.NativeSub, in.Lang))
	case "custom":
		req.OutPath = in.Output.Path
	default:
		if in.ItemID != "" {
			it, ok := s.lookupItem(in.ItemID)
			if !ok {
				writeErr(w, 404, "条目不在缓存中")
				return
			}
			li := s.resolveLocal(it)
			if !li.CanWrite {
				writeErr(w, 400, "影片目录在本机不可访问，请先在设置中配置路径映射，或选择「仅生成下载」")
				return
			}
			req.OutPath = li.OutputPath
		} else {
			req.OutPath = in.Output.Path
		}
	}
	if req.OutPath == "" {
		writeErr(w, 400, "缺少输出路径")
		return
	}
	if in.Output.Mode != "download" && fileExists(req.OutPath) && !in.Overwrite {
		writeJSON(w, 409, map[string]any{"error": "目标文件已存在: " + req.OutPath, "exists": req.OutPath})
		return
	}

	title := in.Title
	if title == "" {
		title = filepath.Base(req.OutPath)
	}
	itemForRefresh := ""
	if in.Refresh && in.Output.Mode != "download" {
		itemForRefresh = in.ItemID
	}
	job, err := s.jobs.Submit(title, itemForRefresh, req)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	if in.Output.Mode == "download" {
		s.jobs.Annotate(job, func(j *pipeline.Job) { j.TempOutput = true })
	}
	writeJSON(w, 200, job)
}

func suffix(native, target string) string {
	if native == "" {
		native = "zh"
	}
	if target == "" {
		return "." + native + ".bilingual.ass"
	}
	return "." + native + "-" + lang.Normalize(target) + ".ass"
}

func sanitize(name string) string {
	if name == "" {
		return "bilingual"
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
}

func (s *Server) pipelineEnv() pipeline.Env {
	cfg := s.config()
	style := cfg.Styling
	p := downloader.NewProviderWithConfig(cfg.Providers)
	return pipeline.Env{
		Tools:    media.NewTools(cfg.FFmpegBin, cfg.FFprobeBin),
		Syncer:   syncer.NewSyncer(cfg.FFsubsyncBin),
		Style:    &style,
		Download: p.Fetch,
		ServerFetch: func(itemID string, idx int) ([]byte, error) {
			adp, err := s.adapter()
			if err != nil {
				return nil, err
			}
			it, ok := s.lookupItem(itemID)
			if !ok {
				return nil, fmt.Errorf("条目 %s 不在缓存中", itemID)
			}
			for _, st := range it.Streams {
				if st.Index == idx && st.Type == "Subtitle" {
					return adp.FetchSubtitle(it, st)
				}
			}
			return nil, fmt.Errorf("条目中没有字幕轨 #%d", idx)
		},
	}
}

func (s *Server) afterJob(job *pipeline.Job) {
	if job.ItemID == "" {
		return
	}
	s.mu.Lock()
	if it, ok := s.items[job.ItemID]; ok {
		it.HasBilingual = true
		s.items[job.ItemID] = it
	}
	s.mu.Unlock()
	adp, err := s.adapter()
	if err != nil {
		return
	}
	err = adp.RefreshItem(job.ItemID)
	s.jobs.Annotate(job, func(j *pipeline.Job) {
		if err != nil {
			j.Logs = append(j.Logs, time.Now().Format("15:04:05 ")+"媒体库刷新失败: "+err.Error())
			return
		}
		j.Refreshed = true
		j.Logs = append(j.Logs, time.Now().Format("15:04:05 ")+"已通知媒体库刷新该条目")
	})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"jobs": s.jobs.List()})
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "任务不存在")
		return
	}
	writeJSON(w, 200, j)
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if !s.jobs.Cancel(r.PathValue("id")) {
		writeErr(w, 400, "任务不可取消")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (s *Server) handleJobDownload(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobs.Get(r.PathValue("id"))
	if !ok || j.Status != pipeline.JobDone {
		http.Error(w, "任务未完成", 404)
		return
	}
	w.Header().Set("Content-Type", "text/x-ssa; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", urlPathEscape(filepath.Base(j.Output))))
	http.ServeFile(w, r, j.Output)
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(indexHTML)
}

type diskCache struct {
	Server   string              `json:"server"`
	CachedAt time.Time           `json:"cached_at"`
	Movies   []adapter.MediaItem `json:"movies"`
}

func (s *Server) cacheFile(key string) string {
	cfg := s.config()
	sum := sha1.Sum([]byte(cfg.Server.Type + "|" + cfg.Server.Host + "|" + key))
	return filepath.Join(filepath.Dir(config.ConfigFilePath()), "cache", fmt.Sprintf("library-%x.json", sum[:6]))
}

func (s *Server) loadDiskCache(key string) *libCache {
	b, err := os.ReadFile(s.cacheFile(key))
	if err != nil {
		return nil
	}
	var dc diskCache
	if json.Unmarshal(b, &dc) != nil || dc.Server != s.config().Server.Host {
		return nil
	}
	return &libCache{movies: dc.Movies, cachedAt: dc.CachedAt}
}

func (s *Server) saveDiskCache(key string, c *libCache) {
	p := s.cacheFile(key)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return
	}
	b, err := json.Marshal(diskCache{Server: s.config().Server.Host, CachedAt: c.cachedAt, Movies: c.movies})
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		os.Rename(tmp, p)
	}
}
