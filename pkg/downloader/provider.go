package downloader

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Gawain12/BabelSub/pkg/config"
)

const maxDownload = 32 << 20

// Provider names as reported in SubtitleSource.Provider.
const (
	SubtitleCat   = "subtitlecat"
	ASSRT         = "assrt"
	OpenSubtitles = "opensubtitles"
)

type SubtitleSource struct {
	Provider    string `json:"provider"`
	Language    string `json:"language"`
	DownloadURL string `json:"download_url"`
	Filename    string `json:"filename"`
	Info        string `json:"info,omitempty"` // free-form details: release group, language mix, downloads…
}

type Provider struct {
	client *http.Client
	cfg    config.ProviderConfig
}

// NewProvider returns a provider with only SubtitleCat enabled (no keys needed).
func NewProvider() *Provider {
	return NewProviderWithConfig(config.ProviderConfig{EnableSubtitleCat: true})
}

func NewProviderWithConfig(cfg config.ProviderConfig) *Provider {
	return &Provider{
		client: &http.Client{Timeout: 30 * time.Second},
		cfg:    cfg,
	}
}

// Enabled lists the providers that will take part in Search, in the order
// their results are returned: human-made sources before SubtitleCat, whose
// non-English files are mostly machine translations.
func (p *Provider) Enabled() []string {
	var out []string
	if p.cfg.EnableASSRT && p.cfg.ASSRTToken != "" {
		out = append(out, ASSRT)
	}
	if p.cfg.EnableOpenSubtitles && p.cfg.OpenSubtitlesKey != "" {
		out = append(out, OpenSubtitles)
	}
	if p.cfg.EnableSubtitleCat {
		out = append(out, SubtitleCat)
	}
	return out
}

// Search queries every enabled provider concurrently. lang is an optional
// language hint (providers that support server-side filtering use it); results
// are not filtered by it here. Errors are reported per provider so one failing
// source does not hide the others.
func (p *Provider) Search(title, lang string) ([]SubtitleSource, map[string]string) {
	names := p.Enabled()
	results := make([][]SubtitleSource, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			switch name {
			case SubtitleCat:
				results[i], errs[i] = p.FetchSubtitleCat(title)
			case ASSRT:
				results[i], errs[i] = p.SearchASSRT(title)
			case OpenSubtitles:
				results[i], errs[i] = p.SearchOpenSubtitles(title, lang)
			}
		}(i, name)
	}
	wg.Wait()

	var all []SubtitleSource
	failed := map[string]string{}
	for i, name := range names {
		if errs[i] != nil {
			failed[name] = errs[i].Error()
		}
		all = append(all, results[i]...)
	}
	return all, failed
}

var (
	scDetailRegex = regexp.MustCompile(`href=["']/?(subs/\d+/[^"'\s]+\.html)["']`)
	scSrtRegex    = regexp.MustCompile(`href=["']/?(subs/\d+/([^"'\s]+-([a-zA-Z0-9-]+)\.srt))["']`)
	scQueryClean  = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// scMaxPages caps how many release pages are opened per search; each release
// page only lists the languages translated for that one release.
const scMaxPages = 4

// FetchSubtitleCat searches SubtitleCat and collects .srt files from the best
// matching release pages.
func (p *Provider) FetchSubtitleCat(title string) ([]SubtitleSource, error) {
	query := strings.TrimSpace(scQueryClean.ReplaceAllString(title, " "))
	if query == "" {
		return nil, fmt.Errorf("搜索词为空")
	}
	body, err := p.getPage("https://www.subtitlecat.com/index.php?search=" + url.QueryEscape(query))
	if err != nil {
		return nil, err
	}

	// Rank release pages: pages whose slug contains every query word first,
	// keeping the site's own order otherwise.
	words := strings.Fields(strings.ToLower(query))
	var exact, rest []string
	seenPage := map[string]bool{}
	for _, m := range scDetailRegex.FindAllStringSubmatch(body, -1) {
		pg := strings.TrimPrefix(m[1], "/")
		if seenPage[pg] {
			continue
		}
		seenPage[pg] = true
		slug := strings.ToLower(pg)
		all := true
		for _, w := range words {
			if !strings.Contains(slug, w) {
				all = false
				break
			}
		}
		if all {
			exact = append(exact, pg)
		} else {
			rest = append(rest, pg)
		}
	}
	pages := append(exact, rest...)
	if len(pages) == 0 {
		return nil, nil
	}
	if len(pages) > scMaxPages {
		pages = pages[:scMaxPages]
	}

	results := make([][]SubtitleSource, len(pages))
	var wg sync.WaitGroup
	for i, pg := range pages {
		wg.Add(1)
		go func(i int, pg string) {
			defer wg.Done()
			detail, err := p.getPage("https://www.subtitlecat.com/" + pg)
			if err != nil {
				return
			}
			for _, m := range scSrtRegex.FindAllStringSubmatch(detail, -1) {
				results[i] = append(results[i], SubtitleSource{
					Provider:    SubtitleCat,
					DownloadURL: "https://www.subtitlecat.com/" + strings.TrimPrefix(m[1], "/"),
					Filename:    m[2],
					Language:    strings.ToLower(m[3]),
				})
			}
		}(i, pg)
	}
	wg.Wait()

	var sources []SubtitleSource
	seen := map[string]bool{}
	for _, list := range results {
		for _, s := range list {
			if !seen[s.DownloadURL] {
				seen[s.DownloadURL] = true
				sources = append(sources, s)
			}
		}
	}
	return sources, nil
}

func (p *Provider) getPage(u string) (string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subtitlecat returned status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(b), err
}

// Fetch materializes a search result as a local subtitle file. dstBase is the
// target path without extension; the returned path carries the real extension
// (.srt / .ass / …). Provider-specific URLs (assrt://, opensubtitles://) are
// resolved to temporary download links first, and archives are unpacked to the
// most suitable subtitle inside.
func (p *Provider) Fetch(rawURL, dstBase string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case ASSRT:
		return p.fetchASSRT(u, dstBase)
	case OpenSubtitles:
		return p.fetchOpenSubtitles(u, dstBase)
	case "http", "https":
		return p.download(rawURL, dstBase)
	}
	return "", fmt.Errorf("unsupported subtitle URL: %s", rawURL)
}

// download saves url to dstBase+ext and unpacks archives.
func (p *Provider) download(rawURL, dstBase string) (string, error) {
	req, err := http.NewRequest("GET", escapeURL(rawURL), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxDownload {
		return "", fmt.Errorf("download exceeds %d MB", maxDownload>>20)
	}

	name := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	if name == "" {
		name = path.Base(resp.Request.URL.Path)
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case isZip(data):
		return extractZip(data, dstBase)
	case isRar(data), is7z(data):
		return "", fmt.Errorf("不支持 rar / 7z 压缩包: %s", name)
	case !isSubtitleExt(ext):
		ext = ".srt"
	}
	out := dstBase + ext
	if err := os.WriteFile(out, data, 0644); err != nil {
		return "", err
	}
	return out, nil
}

// escapeURL re-encodes URLs that arrive with raw spaces or non-ASCII paths
// (ASSRT returns them like that).
func escapeURL(raw string) string {
	u, err := url.Parse(strings.ReplaceAll(raw, " ", "%20"))
	if err != nil {
		return raw
	}
	u.RawPath = ""
	return u.String()
}
