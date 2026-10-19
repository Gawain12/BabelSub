package downloader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Gawain12/BabelSub/pkg/lang"
)

// OpenSubtitles.com REST API (v1). Searching only needs the consumer API key;
// downloads are counted against a small daily quota per key / IP. Results carry
// opensubtitles://<file_id> and are exchanged for a temporary link on download.

var openSubtitlesBase = "https://api.opensubtitles.com/api/v1"

const openSubtitlesUA = "BabelSub v1"

type osSearchResp struct {
	Data []struct {
		Attributes struct {
			Language          string `json:"language"`
			Release           string `json:"release"`
			DownloadCount     int    `json:"download_count"`
			HearingImpaired   bool   `json:"hearing_impaired"`
			MachineTranslated bool   `json:"machine_translated"`
			AITranslated      bool   `json:"ai_translated"`
			FeatureDetails    struct {
				Title string `json:"title"`
				Year  int    `json:"year"`
			} `json:"feature_details"`
			Files []struct {
				FileID   int    `json:"file_id"`
				FileName string `json:"file_name"`
			} `json:"files"`
		} `json:"attributes"`
	} `json:"data"`
}

func (p *Provider) osRequest(method, endpoint string, body any) (*http.Response, error) {
	if p.cfg.OpenSubtitlesKey == "" {
		return nil, fmt.Errorf("未配置 OpenSubtitles API key")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, openSubtitlesBase+endpoint, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", p.cfg.OpenSubtitlesKey)
	req.Header.Set("User-Agent", openSubtitlesUA)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(msg, &e) == nil && e.Message != "" {
			return nil, fmt.Errorf("OpenSubtitles: %s (HTTP %d)", e.Message, resp.StatusCode)
		}
		return nil, fmt.Errorf("OpenSubtitles: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// osLanguages maps a language hint to OpenSubtitles' codes.
func osLanguages(l string) string {
	switch c := lang.Normalize(l); c {
	case "":
		return ""
	case "zh":
		return "zh-cn,zh-tw"
	case "pt":
		return "pt-br,pt-pt"
	default:
		return c
	}
}

// SearchOpenSubtitles returns one result per subtitle file. langHint narrows
// the search server-side when set.
func (p *Provider) SearchOpenSubtitles(title, langHint string) ([]SubtitleSource, error) {
	// The API redirects requests whose parameters are not lower-case and sorted
	// (url.Values.Encode sorts keys).
	args := url.Values{"query": {strings.ToLower(strings.TrimSpace(title))}}
	if l := osLanguages(langHint); l != "" {
		args.Set("languages", l)
	}
	resp, err := p.osRequest("GET", "/subtitles?"+args.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r osSearchResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("OpenSubtitles: %w", err)
	}
	var out []SubtitleSource
	for _, d := range r.Data {
		a := d.Attributes
		var info []string
		if a.FeatureDetails.Title != "" {
			t := a.FeatureDetails.Title
			if a.FeatureDetails.Year > 0 {
				t += fmt.Sprintf(" (%d)", a.FeatureDetails.Year)
			}
			info = append(info, t)
		}
		info = append(info, fmt.Sprintf("%d 次下载", a.DownloadCount))
		if a.MachineTranslated || a.AITranslated {
			info = append(info, "机翻")
		}
		if a.HearingImpaired {
			info = append(info, "SDH")
		}
		for _, f := range a.Files {
			name := f.FileName
			if name == "" {
				name = a.Release
			}
			out = append(out, SubtitleSource{
				Provider:    OpenSubtitles,
				Language:    strings.ToLower(a.Language),
				DownloadURL: fmt.Sprintf("opensubtitles://%d", f.FileID),
				Filename:    name,
				Info:        strings.Join(info, " · "),
			})
		}
	}
	return out, nil
}

func (p *Provider) fetchOpenSubtitles(u *url.URL, dstBase string) (string, error) {
	id, err := strconv.Atoi(u.Host)
	if err != nil {
		return "", fmt.Errorf("invalid OpenSubtitles file id: %s", u.Host)
	}
	resp, err := p.osRequest("POST", "/download", map[string]any{"file_id": id})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct {
		Link     string `json:"link"`
		FileName string `json:"file_name"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("OpenSubtitles: %w", err)
	}
	if r.Link == "" {
		return "", fmt.Errorf("OpenSubtitles: 未返回下载链接 %s", r.Message)
	}
	return p.download(r.Link, dstBase)
}
