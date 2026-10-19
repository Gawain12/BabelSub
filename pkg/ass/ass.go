package ass

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gawaintan/subsync-studio/pkg/config"
)

type SubtitleEntry struct {
	Index int
	Start time.Duration
	End   time.Duration
	Text  string
}

func ParseSRT(filePath string) ([]SubtitleEntry, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var entries []SubtitleEntry
	timeRegex := regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2}),(\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2}),(\d{3})`)

	var curEntry *SubtitleEntry
	var lines []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if curEntry != nil && len(lines) > 0 {
				curEntry.Text = strings.Join(lines, " ")
				entries = append(entries, *curEntry)
				curEntry = nil
				lines = nil
			}
			continue
		}

		if curEntry == nil {
			matches := timeRegex.FindStringSubmatch(line)
			if len(matches) == 9 {
				start := parseTime(matches[1], matches[2], matches[3], matches[4])
				end := parseTime(matches[5], matches[6], matches[7], matches[8])
				curEntry = &SubtitleEntry{Start: start, End: end}
				continue
			}
			if _, err := strconv.Atoi(line); err == nil {
				continue
			}
		} else {
			lines = append(lines, line)
		}
	}

	if curEntry != nil && len(lines) > 0 {
		curEntry.Text = strings.Join(lines, " ")
		entries = append(entries, *curEntry)
	}

	return entries, nil
}

func parseTime(h, m, s, ms string) time.Duration {
	hours, _ := strconv.Atoi(h)
	mins, _ := strconv.Atoi(m)
	secs, _ := strconv.Atoi(s)
	milli, _ := strconv.Atoi(ms)
	return time.Duration(hours)*time.Hour +
		time.Duration(mins)*time.Minute +
		time.Duration(secs)*time.Second +
		time.Duration(milli)*time.Millisecond
}

func formatASSTime(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	cs := int(d.Milliseconds()%1000) / 10
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

func GenerateBilingualASS(primarySRT, secondarySRT, outPath string, style *config.StyleConfig) error {
	if style == nil {
		style = &config.DefaultConfig().Styling
	}

	prim, err := ParseSRT(primarySRT)
	if err != nil {
		return fmt.Errorf("failed to parse primary SRT: %w", err)
	}
	sec, err := ParseSRT(secondarySRT)
	if err != nil {
		return fmt.Errorf("failed to parse secondary SRT: %w", err)
	}

	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	w := bufio.NewWriter(outFile)

	// Note: Explicit PlayResX/PlayResY to guarantee correct non-stretched font scale across players like Infuse
	header := fmt.Sprintf(`[Script Info]
Title: SubSync Studio Bilingual Subtitle
ScriptType: v4.00+
WrapStyle: 0
ScaledBorderAndShadow: yes
YCbCr Matrix: TV.601
PlayResX: %d
PlayResY: %d

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,%s,%d,%s,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,2.0,1.0,2,30,30,%d,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`,
		style.PlayResX,
		style.PlayResY,
		style.FontName,
		style.PrimaryFontSize,
		style.PrimaryColor,
		style.MarginV,
	)
	w.WriteString(header)

	for _, p := range prim {
		var matchedSec []string
		for _, s := range sec {
			overlapStart := p.Start
			if s.Start > overlapStart {
				overlapStart = s.Start
			}
			overlapEnd := p.End
			if s.End < overlapEnd {
				overlapEnd = s.End
			}
			if overlapEnd > overlapStart {
				clean := strings.TrimSpace(s.Text)
				if clean != "" {
					matchedSec = append(matchedSec, clean)
				}
			}
		}

		secText := strings.Join(matchedSec, " / ")
		var text string
		if secText != "" {
			// Clean stacked formatting with proportional scaling
			text = fmt.Sprintf("%s\\N{\\fscx82\\fscy82\\c%s}%s", p.Text, style.SecondaryColor, secText)
		} else {
			text = p.Text
		}

		line := fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n",
			formatASSTime(p.Start),
			formatASSTime(p.End),
			text)
		w.WriteString(line)
	}

	return w.Flush()
}
