// Package lang normalizes language codes coming from different media servers
// (Emby uses ISO 639-2 like "jpn", Plex uses "ja" or "jpn", ffprobe uses "jpn").
package lang

import "strings"

type Info struct {
	Code      string   // canonical ISO 639-1 code, e.g. "ja"
	Name      string   // display name
	Aliases   []string // codes / names seen in the wild
	Countries []string // production countries strongly associated with the language
}

var table = []Info{
	{"ja", "日语", []string{"ja", "jpn", "japanese", "日本語"}, []string{"japan", "日本"}},
	{"en", "英语", []string{"en", "eng", "english"}, []string{"united states", "united kingdom", "australia", "canada", "new zealand", "ireland", "美国", "英国"}},
	{"zh", "中文", []string{"zh", "chi", "zho", "chs", "cht", "chinese", "cmn", "yue", "zh-cn", "zh-tw", "zh-hans", "zh-hant", "cn", "tw"}, []string{"china", "taiwan", "hong kong", "中国", "台湾", "香港"}},
	{"ko", "韩语", []string{"ko", "kor", "korean"}, []string{"south korea", "korea", "韩国"}},
	{"fr", "法语", []string{"fr", "fre", "fra", "french"}, []string{"france", "法国"}},
	{"de", "德语", []string{"de", "ger", "deu", "german"}, []string{"germany", "austria", "德国"}},
	{"es", "西班牙语", []string{"es", "spa", "spanish", "419", "es-419"}, []string{"spain", "mexico", "argentina", "西班牙"}},
	{"it", "意大利语", []string{"it", "ita", "italian"}, []string{"italy", "意大利"}},
	{"ru", "俄语", []string{"ru", "rus", "russian"}, []string{"russia", "soviet union", "俄罗斯", "苏联"}},
	{"pt", "葡萄牙语", []string{"pt", "por", "portuguese", "br", "pt-br"}, []string{"portugal", "brazil", "葡萄牙", "巴西"}},
	{"th", "泰语", []string{"th", "tha", "thai"}, []string{"thailand", "泰国"}},
}

// Lookup returns the language info for any known alias. Unknown codes yield an
// Info whose only alias is the code itself.
func Lookup(code string) Info {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return Info{}
	}
	for _, l := range table {
		for _, a := range l.Aliases {
			if a == c {
				return l
			}
		}
	}
	return Info{Code: c, Name: c, Aliases: []string{c}}
}

// Normalize maps an arbitrary code to its canonical 2-letter form.
func Normalize(code string) string { return Lookup(code).Code }

// Matches reports whether two language codes refer to the same language.
func Matches(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return Normalize(a) == Normalize(b)
}

// MatchesCountry reports whether the primary (first listed) production
// location belongs to the language. Minor co-producers are ignored so that,
// e.g., a US film with Japanese co-financing is not treated as Japanese.
func MatchesCountry(code string, locations []string) bool {
	if len(locations) == 0 {
		return false
	}
	l := strings.ToLower(locations[0])
	for _, c := range Lookup(code).Countries {
		if strings.Contains(l, c) {
			return true
		}
	}
	return false
}

// IsBilingualName reports whether a subtitle title or path looks like a bilingual
// file produced for this language pair (e.g. "movie.zh-ja.ass", "中日双语").
func IsBilingualName(s, native, target string) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "双语") || strings.Contains(s, "bilingual") {
		return true
	}
	n, t := Normalize(native), Normalize(target)
	if n == "" {
		n = "zh"
	}
	if t != "" {
		return strings.Contains(s, n+"-"+t) || strings.Contains(s, t+"-"+n)
	}
	return strings.Contains(s, n+"-")
}
