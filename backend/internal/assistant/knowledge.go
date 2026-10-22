package assistant

import (
	"embed"
	"io/fs"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The assistant's knowledge of PortDock: the user documentation, embedded in
// the binary and searched locally (no index service, no network). docs/README.md
// and docs/service-templates.md are copies of the repository's README files,
// kept in sync by `make docs-sync` and checked by TestDocsInSync;
// docs/assistant-guide.md is written for the assistant itself.

//go:embed docs/*.md
var docsFS embed.FS

// DocSection is the text under one heading of the documentation.
type DocSection struct {
	Source string `json:"source"` // file, e.g. README.md
	Path   string `json:"path"`   // heading path, e.g. "PortDock documentation › Database backups"
	Text   string `json:"text"`

	terms  map[string]int
	length int
}

// SectionMaxChars bounds the text of a section returned by SearchDocs.
const SectionMaxChars = 3500

var (
	kbOnce     sync.Once
	kbSections []*DocSection
	kbDF       map[string]int
	kbAvgLen   float64
)

func knowledge() ([]*DocSection, map[string]int, float64) {
	kbOnce.Do(func() {
		entries, _ := fs.ReadDir(docsFS, "docs")
		kbDF = map[string]int{}
		total := 0
		for _, e := range entries {
			b, err := docsFS.ReadFile("docs/" + e.Name())
			if err != nil {
				continue
			}
			for _, sec := range SplitSections(e.Name(), string(b)) {
				sec.terms = map[string]int{}
				for _, t := range Tokenize(sec.Text) {
					sec.terms[t]++
					sec.length++
				}
				for _, t := range Tokenize(sec.Path) { // headings count double
					sec.terms[t] += 2
					sec.length += 2
				}
				for t := range sec.terms {
					kbDF[t]++
				}
				total += sec.length
				kbSections = append(kbSections, sec)
			}
		}
		if len(kbSections) > 0 {
			kbAvgLen = float64(total) / float64(len(kbSections))
		}
	})
	return kbSections, kbDF, kbAvgLen
}

// DocSources lists the embedded documentation files.
func DocSources() []string {
	entries, _ := fs.ReadDir(docsFS, "docs")
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

var (
	summaryRe   = regexp.MustCompile(`<summary>\s*(?:<b>)?(.*?)(?:</b>)?\s*</summary>`)
	htmlTagRe   = regexp.MustCompile(`<[^>]+>`)
	boldLineRe  = regexp.MustCompile(`^\*\*(.+)\*\*$`)
	headingRe   = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	dropLineRe  = regexp.MustCompile(`^\s*(<(/?)(div|picture|source|img|details|table|tr|td|br)\b[^>]*>\s*)+$|^\s*\[!\[`)
	mdMarkupRe  = regexp.MustCompile("[*_`]")
	spaceRe     = regexp.MustCompile(`[ \t]+`)
	manyBlankRe = regexp.MustCompile(`\n{3,}`)
)

// SplitSections cuts a Markdown document into sections, one per heading
// (Markdown headings, <summary> titles and bold stand-alone lines).
func SplitSections(source, text string) []*DocSection {
	label := strings.TrimSuffix(source, ".md")
	switch source {
	case "README.md":
		label = "PortDock documentation"
	case "assistant-guide.md":
		label = "Assistant guide"
	case "service-templates.md":
		label = "Service templates"
	}
	var out []*DocSection
	var stack []string // heading titles, outermost first
	var buf strings.Builder
	flush := func() {
		body := strings.TrimSpace(manyBlankRe.ReplaceAllString(buf.String(), "\n\n"))
		buf.Reset()
		if body == "" {
			return
		}
		path := label
		if len(stack) > 0 {
			path += " › " + strings.Join(stack, " › ")
		}
		out = append(out, &DocSection{Source: source, Path: path, Text: body})
	}
	levels := []int{} // heading level of each entry of stack
	setLevel := func(level int, title string) {
		title = strings.TrimSpace(mdMarkupRe.ReplaceAllString(htmlTagRe.ReplaceAllString(title, ""), ""))
		for len(levels) > 0 && levels[len(levels)-1] >= level {
			levels = levels[:len(levels)-1]
			stack = stack[:len(stack)-1]
		}
		levels = append(levels, level)
		stack = append(stack, title)
	}
	inCode := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			buf.WriteString(line + "\n")
			continue
		}
		if inCode {
			buf.WriteString(line + "\n")
			continue
		}
		if m := summaryRe.FindStringSubmatch(trimmed); m != nil {
			flush()
			setLevel(3, m[1])
			continue
		}
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			flush()
			setLevel(len(m[1]), m[2])
			continue
		}
		if m := boldLineRe.FindStringSubmatch(trimmed); m != nil && len(m[1]) < 60 {
			flush()
			setLevel(3, m[1])
			continue
		}
		if dropLineRe.MatchString(trimmed) {
			continue
		}
		line = htmlTagRe.ReplaceAllString(line, "")
		buf.WriteString(spaceRe.ReplaceAllString(line, " ") + "\n")
	}
	flush()
	return out
}

var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "to": true, "in": true, "on": true, "for": true, "and": true, "or": true,
	"is": true, "are": true, "be": true, "as": true, "by": true, "at": true, "from": true, "with": true, "it": true, "its": true,
	"this": true, "that": true, "these": true, "those": true, "i": true, "my": true, "me": true, "we": true, "you": true, "your": true,
	"can": true, "do": true, "does": true, "how": true, "what": true, "why": true, "when": true, "which": true, "who": true,
	"not": true, "no": true, "yes": true, "if": true, "then": true, "than": true, "so": true, "but": true, "into": true, "onto": true,
	"one": true, "any": true, "all": true, "each": true, "every": true, "also": true, "only": true, "same": true, "other": true,
	"up": true, "out": true, "about": true, "after": true, "before": true, "over": true, "under": true, "between": true,
	"has": true, "have": true, "had": true, "was": true, "were": true, "will": true, "would": true, "should": true, "could": true,
	"there": true, "here": true, "where": true, "they": true, "them": true, "their": true, "he": true, "she": true, "his": true, "her": true,
	"please": true, "want": true, "need": true, "like": true, "get": true, "use": true, "using": true, "used": true, "via": true,
}

// Tokenize lowercases the text and splits it into search terms. Words keep
// their inner dots, dashes, slashes and underscores (docker-compose.yml,
// pg_dump, /api/metrics) and their parts are indexed too; a crude stemmer
// folds plurals and -ing/-ed forms.
func Tokenize(text string) []string {
	var out []string
	var cur strings.Builder
	emit := func() {
		if cur.Len() == 0 {
			return
		}
		w := strings.Trim(cur.String(), "._-/")
		cur.Reset()
		if w == "" {
			return
		}
		if strings.ContainsAny(w, "/-._") {
			for _, part := range strings.FieldsFunc(w, func(r rune) bool { return r == '/' || r == '-' || r == '.' || r == '_' }) {
				if t := stem(part); t != "" {
					out = append(out, t)
				}
			}
		}
		if t := stem(w); t != "" {
			out = append(out, t)
		}
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127 && isLetter(r):
			cur.WriteRune(r)
		case r == '.' || r == '-' || r == '/' || r == '_':
			cur.WriteRune(r)
		default:
			emit()
		}
	}
	emit()
	return out
}

func isLetter(r rune) bool {
	return (r >= 'à' && r <= 'ÿ') || (r >= 'ă' && r <= 'ț') || (r >= 'а' && r <= 'я') || r == 'ё' || r == 'ș' || r == 'ț' || r == 'â' || r == 'î'
}

func stem(w string) string {
	if len(w) < 2 || stopWords[w] {
		return ""
	}
	if n := len(w); n > 4 {
		switch {
		case strings.HasSuffix(w, "ies"):
			w = w[:n-3] + "y"
		case strings.HasSuffix(w, "sses"), strings.HasSuffix(w, "xes"), strings.HasSuffix(w, "ches"), strings.HasSuffix(w, "shes"):
			w = w[:n-2]
		case strings.HasSuffix(w, "ing") && n > 5:
			w = w[:n-3]
		case strings.HasSuffix(w, "ed") && n > 5:
			w = w[:n-2]
		case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
			w = w[:n-1]
		}
	}
	return w
}

// SearchDocs ranks the documentation sections for a query (BM25 over the
// terms, with a bonus when the whole phrase occurs) and returns the best
// ones, their text clipped to SectionMaxChars.
func SearchDocs(query string, limit int) []DocSection {
	sections, df, avg := knowledge()
	if limit <= 0 {
		limit = 4
	}
	qterms := Tokenize(query)
	if len(qterms) == 0 || len(sections) == 0 {
		return nil
	}
	seen := map[string]bool{}
	uniq := qterms[:0:0]
	for _, t := range qterms {
		if !seen[t] {
			seen[t] = true
			uniq = append(uniq, t)
		}
	}
	phrase := strings.ToLower(strings.Join(strings.Fields(query), " "))
	n := float64(len(sections))
	const k1, b = 1.4, 0.6
	type hit struct {
		sec   *DocSection
		score float64
	}
	var hits []hit
	for _, sec := range sections {
		var score float64
		for _, t := range uniq {
			tf := float64(sec.terms[t])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			norm := 1 - b + b*float64(sec.length)/math.Max(avg, 1)
			score += idf * tf * (k1 + 1) / (tf + k1*norm)
		}
		if score == 0 {
			continue
		}
		if len(phrase) > 3 && strings.Contains(strings.ToLower(sec.Path+"\n"+sec.Text), phrase) {
			score += 1.5
		}
		hits = append(hits, hit{sec, score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]DocSection, 0, len(hits))
	for _, h := range hits {
		s := DocSection{Source: h.sec.Source, Path: h.sec.Path, Text: h.sec.Text}
		if r := []rune(s.Text); len(r) > SectionMaxChars {
			s.Text = string(r[:SectionMaxChars]) + "\n…(the section continues; search a more specific topic for the rest)"
		}
		out = append(out, s)
	}
	return out
}
