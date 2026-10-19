package downloader

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ASSRT (assrt.net, 射手网(伪)) has the largest collection of fansub Chinese
// subtitles. The API needs a free per-user token and allows 20 requests/min.
// Download links expire, so results carry assrt://<id> and are resolved via
// sub/detail at download time.

var assrtBase = "https://api.assrt.net/v1"

type assrtSub struct {
	ID          int    `json:"id"`
	NativeName  string `json:"native_name"`
	VideoName   string `json:"videoname"`
	Subtype     string `json:"subtype"`
	ReleaseSite string `json:"release_site"`
	UploadTime  string `json:"upload_time"`
	Lang        struct {
		Desc     string          `json:"desc"`
		LangList map[string]bool `json:"langlist"`
	} `json:"lang"`
	// detail only
	Filename string `json:"filename"`
	URL      string `json:"url"`
	FileList []struct {
		URL  string `json:"url"`
		Name string `json:"f"`
		Size string `json:"s"`
	} `json:"filelist"`
}

type assrtResp struct {
	Status int    `json:"status"`
	ErrMsg string `json:"errmsg"`
	Sub    struct {
		Subs []assrtSub `json:"subs"`
	} `json:"sub"`
}

func (p *Provider) assrtCall(endpoint string, args url.Values) ([]assrtSub, error) {
	if p.cfg.ASSRTToken == "" {
		return nil, fmt.Errorf("未配置 ASSRT token")
	}
	req, err := http.NewRequest("GET", assrtBase+endpoint+"?"+args.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.ASSRTToken)
	req.Header.Set("User-Agent", "BabelSub")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r assrtResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("ASSRT: HTTP %d: %w", resp.StatusCode, err)
	}
	if r.Status != 0 {
		return nil, fmt.Errorf("ASSRT: %s (%d)", r.ErrMsg, r.Status)
	}
	return r.Sub.Subs, nil
}

// SearchASSRT returns one result per ASSRT subtitle package.
func (p *Provider) SearchASSRT(title string) ([]SubtitleSource, error) {
	q := strings.TrimSpace(title)
	if len([]rune(q)) < 3 {
		return nil, fmt.Errorf("ASSRT: 搜索词至少 3 个字符")
	}
	subs, err := p.assrtCall("/sub/search", url.Values{"q": {q}, "cnt": {"15"}})
	if err != nil {
		return nil, err
	}
	var out []SubtitleSource
	for _, s := range subs {
		name := s.VideoName
		if name == "" {
			name = s.NativeName
		}
		info := []string{}
		for _, x := range []string{s.Lang.Desc, s.Subtype, s.ReleaseSite} {
			if x = strings.TrimSpace(x); x != "" {
				info = append(info, x)
			}
		}
		if s.NativeName != "" && s.NativeName != name {
			info = append(info, s.NativeName)
		}
		out = append(out, SubtitleSource{
			Provider:    ASSRT,
			Language:    assrtLanguage(s.Lang.LangList),
			DownloadURL: fmt.Sprintf("assrt://%d", s.ID),
			Filename:    name,
			Info:        strings.Join(info, " · "),
		})
	}
	return out, nil
}

// assrtLanguage maps ASSRT's langlist flags (langchs, langcht, langdou=双语,
// langeng, langjap, langkor…) to one code. Anything with Chinese counts as zh,
// since that is what this site is used for.
func assrtLanguage(list map[string]bool) string {
	for _, k := range []string{"langchs", "langcht", "langdou"} {
		if list[k] {
			return "zh"
		}
	}
	codes := map[string]string{"eng": "en", "jap": "ja", "jpn": "ja", "kor": "ko", "fre": "fr", "fra": "fr", "ger": "de", "deu": "de", "rus": "ru", "spa": "es", "ita": "it", "por": "pt", "tha": "th"}
	keys := make([]string, 0, len(list))
	for k, ok := range list {
		if ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := strings.TrimPrefix(k, "lang")
		if v, found := codes[c]; found {
			return v
		}
		return c
	}
	return "zh"
}

// fetchASSRT resolves assrt://<id>[?file=<name in archive>] and downloads the
// best subtitle. The per-file "onthefly" links let us skip rar/7z archives.
func (p *Provider) fetchASSRT(u *url.URL, dstBase string) (string, error) {
	id, err := strconv.Atoi(u.Host)
	if err != nil {
		return "", fmt.Errorf("invalid ASSRT id: %s", u.Host)
	}
	subs, err := p.assrtCall("/sub/detail", url.Values{"id": {strconv.Itoa(id)}})
	if err != nil {
		return "", err
	}
	if len(subs) == 0 {
		return "", fmt.Errorf("ASSRT: 字幕 %d 不存在", id)
	}
	s := subs[0]
	if want := u.Query().Get("file"); want != "" {
		for _, f := range s.FileList {
			if f.Name == want {
				return p.download(f.URL, dstBase)
			}
		}
		return "", fmt.Errorf("ASSRT: 字幕包内没有 %s", want)
	}
	names := make([]string, len(s.FileList))
	for i, f := range s.FileList {
		names[i] = f.Name
	}
	if i := bestSubtitle(names); i >= 0 {
		return p.download(s.FileList[i].URL, dstBase)
	}
	if s.URL == "" {
		return "", fmt.Errorf("ASSRT: 字幕 %d 没有可下载的 srt / ass 文件", id)
	}
	return p.download(s.URL, dstBase)
}
