package downloader

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type SubtitleSource struct {
	Language    string `json:"language"`
	DownloadURL string `json:"download_url"`
	Filename    string `json:"filename"`
}

type Provider struct {
	client *http.Client
}

func NewProvider() *Provider {
	return &Provider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// FetchSubtitleCat searches SubtitleCat for candidate subtitle files (.srt)
func (p *Provider) FetchSubtitleCat(title string) ([]SubtitleSource, error) {
	searchURL := fmt.Sprintf("https://www.subtitlecat.com/index.php?search=%s", url.QueryEscape(title))
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	bodyStr := string(body)
	// Find the most relevant matching detail page link
	linkRegex := regexp.MustCompile(`href=["']/?(subs/\d+/[^"'\s]+\.html)["']`)
	allMatches := linkRegex.FindAllStringSubmatch(bodyStr, -1)
	if len(allMatches) == 0 {
		return nil, fmt.Errorf("no results found on SubtitleCat for %s", title)
	}

	// Pick best match containing title keywords
	chosenPath := allMatches[0][1]
	normalizedQuery := strings.ToLower(strings.ReplaceAll(title, " ", "."))
	for _, m := range allMatches {
		if strings.Contains(strings.ToLower(m[1]), normalizedQuery) {
			chosenPath = m[1]
			break
		}
	}

	detailURL := "https://www.subtitlecat.com/" + strings.TrimPrefix(chosenPath, "/")
	reqDetail, _ := http.NewRequest("GET", detailURL, nil)
	reqDetail.Header.Set("User-Agent", "Mozilla/5.0")
	respDetail, err := p.client.Do(reqDetail)
	if err != nil {
		return nil, err
	}
	defer respDetail.Body.Close()

	detailBody, _ := io.ReadAll(respDetail.Body)
	srtRegex := regexp.MustCompile(`href=["']/?(subs/\d+/([^"'\s]+-([a-zA-Z0-9-]+)\.srt))["']`)
	srtMatches := srtRegex.FindAllStringSubmatch(string(detailBody), -1)

	var sources []SubtitleSource
	seen := make(map[string]bool)
	for _, m := range srtMatches {
		if len(m) >= 4 {
			fullURL := "https://www.subtitlecat.com/" + strings.TrimPrefix(m[1], "/")
			if seen[fullURL] {
				continue
			}
			seen[fullURL] = true
			sources = append(sources, SubtitleSource{
				DownloadURL: fullURL,
				Filename:    m[2],
				Language:    strings.ToLower(m[3]),
			})
		}
	}

	return sources, nil
}

// DownloadFile saves a remote URL to targetPath
func (p *Provider) DownloadFile(downloadURL, targetPath string) error {
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	f, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
