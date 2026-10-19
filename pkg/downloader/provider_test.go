package downloader

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gawain12/BabelSub/pkg/config"
)

const srtBody = "1\n00:00:01,000 --> 00:00:02,000\n你好\n"

func zipOf(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func TestBestSubtitle(t *testing.T) {
	names := []string{
		"Swing.Girls.cht.srt",
		"Swing.Girls.chs&jpn.ass",
		"readme.txt",
		"Swing.Girls.chs.srt",
		"__MACOSX/._Swing.Girls.chs.srt",
	}
	if got := names[bestSubtitle(names)]; got != "Swing.Girls.chs.srt" {
		t.Fatalf("best = %s", got)
	}
	if bestSubtitle([]string{"a.txt", "b.nfo"}) != -1 {
		t.Fatal("expected no subtitle")
	}
}

func TestASSRT(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" && !strings.HasPrefix(r.URL.Path, "/file") {
			w.Write([]byte(`{"status":20001,"errmsg":"invalid token"}`))
			return
		}
		switch r.URL.Path {
		case "/v1/sub/search":
			w.Write([]byte(`{"status":0,"sub":{"subs":[
				{"id":601234,"native_name":"摇摆少女/Swing Girls","videoname":"Swing.Girls.2004.720p","subtype":"Subrip(srt)","release_site":"个人","lang":{"desc":"简 繁","langlist":{"langchs":true,"langcht":true}}},
				{"id":601235,"native_name":"Swing Girls","videoname":"","lang":{"desc":"日","langlist":{"langjap":true}}}]}}`))
		case "/v1/sub/detail":
			if r.URL.Query().Get("id") == "601234" {
				// archive only (zip), no per-file list
				fmt.Fprintf(w, `{"status":0,"sub":{"subs":[{"id":601234,"url":"%s/file/Swing Girls 字幕.zip?api=1","filelist":[]}]}}`, srv.URL)
			} else {
				fmt.Fprintf(w, `{"status":0,"sub":{"subs":[{"id":601235,"url":"%s/file/x.rar","filelist":[
					{"url":"%s/file/onthefly/Swing.Girls.cht.srt","f":"Swing.Girls.cht.srt"},
					{"url":"%s/file/onthefly/Swing.Girls.chs.srt","f":"Swing.Girls.chs.srt"}]}]}}`, srv.URL, srv.URL, srv.URL)
			}
		case "/file/Swing Girls 字幕.zip":
			w.Write(zipOf(t, map[string]string{"Swing.Girls.cht.ass": "x", "Swing.Girls.chs.srt": srtBody, "info.txt": "x"}))
		case "/file/onthefly/Swing.Girls.chs.srt":
			w.Write([]byte(srtBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := assrtBase
	assrtBase = srv.URL + "/v1"
	defer func() { assrtBase = old }()

	p := NewProviderWithConfig(config.ProviderConfig{EnableASSRT: true, ASSRTToken: "tok"})
	res, errs := p.Search("Swing Girls", "")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(res) != 2 || res[0].Provider != ASSRT || res[0].Language != "zh" || res[0].DownloadURL != "assrt://601234" || res[1].Language != "ja" {
		t.Fatalf("unexpected results: %+v", res)
	}
	if res[1].Filename != "Swing Girls" {
		t.Fatalf("filename fallback: %q", res[1].Filename)
	}

	dir := t.TempDir()
	for i, src := range res {
		out, err := p.Fetch(src.DownloadURL, filepath.Join(dir, fmt.Sprint(i)))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(out)
		if filepath.Ext(out) != ".srt" || string(b) != srtBody {
			t.Fatalf("fetch %s -> %s %q", src.DownloadURL, out, b)
		}
	}

	bad := NewProviderWithConfig(config.ProviderConfig{EnableASSRT: true, ASSRTToken: "nope"})
	if _, errs := bad.Search("Swing Girls", ""); !strings.Contains(errs[ASSRT], "invalid token") {
		t.Fatalf("expected token error, got %v", errs)
	}
}

func TestOpenSubtitles(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dl/") {
			w.Header().Set("Content-Disposition", `attachment; filename="Swing.Girls.ja.srt"`)
			w.Write([]byte(srtBody))
			return
		}
		if r.Header.Get("Api-Key") != "key" || r.Header.Get("User-Agent") == "" {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"You cannot consume this service"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/subtitles":
			if r.URL.Query().Get("languages") != "ja" {
				t.Errorf("languages = %q", r.URL.Query().Get("languages"))
			}
			w.Write([]byte(`{"data":[{"attributes":{"language":"ja","release":"Swing.Girls.2004","download_count":120,
				"feature_details":{"title":"Swing Girls","year":2004},"files":[{"file_id":42,"file_name":"Swing.Girls.2004.ja"}]}}]}`))
		case "/api/v1/download":
			var body struct {
				FileID int `json:"file_id"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if r.Method != "POST" || body.FileID != 42 {
				t.Errorf("download %s %d", r.Method, body.FileID)
			}
			fmt.Fprintf(w, `{"link":"%s/dl/abc","file_name":"Swing.Girls.ja.srt","remaining":4}`, srv.URL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := openSubtitlesBase
	openSubtitlesBase = srv.URL + "/api/v1"
	defer func() { openSubtitlesBase = old }()

	p := NewProviderWithConfig(config.ProviderConfig{EnableOpenSubtitles: true, OpenSubtitlesKey: "key"})
	res, errs := p.Search("Swing Girls", "ja")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(res) != 1 || res[0].Provider != OpenSubtitles || res[0].DownloadURL != "opensubtitles://42" || !strings.Contains(res[0].Info, "Swing Girls (2004)") {
		t.Fatalf("unexpected results: %+v", res)
	}
	out, err := p.Fetch(res[0].DownloadURL, filepath.Join(t.TempDir(), "os"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(out) != ".srt" {
		t.Fatalf("out = %s", out)
	}

	bad := NewProviderWithConfig(config.ProviderConfig{EnableOpenSubtitles: true, OpenSubtitlesKey: "wrong"})
	if _, errs := bad.Search("Swing Girls", "ja"); !strings.Contains(errs[OpenSubtitles], "cannot consume") {
		t.Fatalf("expected key error, got %v", errs)
	}
}

func TestEnabledNeedsKeys(t *testing.T) {
	p := NewProviderWithConfig(config.ProviderConfig{EnableSubtitleCat: true, EnableASSRT: true, EnableOpenSubtitles: true})
	if got := strings.Join(p.Enabled(), ","); got != SubtitleCat {
		t.Fatalf("enabled = %s", got)
	}
}
