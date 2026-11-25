package importer

import (
	"regexp"
	"strings"
)

// Testo di Jira (wiki markup, nei CSV e nell'API v2) → Markdown, per i costrutti più comuni:
// titoli, grassetto, corsivo, codice, citazioni, elenchi, link e menzioni. Quello che non si
// riconosce resta com'è: si legge comunque.

var (
	wikiHeadingRe  = regexp.MustCompile(`(?m)^h([1-6])\.\s+`)
	wikiCodeRe     = regexp.MustCompile(`(?s)\{code(?::([a-zA-Z0-9_+-]+))?[^}]*\}(.*?)\{code\}`)
	wikiNoformatRe = regexp.MustCompile(`(?s)\{noformat\}(.*?)\{noformat\}`)
	wikiQuoteRe    = regexp.MustCompile(`(?s)\{quote\}(.*?)\{quote\}`)
	wikiLinkRe     = regexp.MustCompile(`\[([^\]|\n]+)\|((?:https?|mailto):[^\]\s]+)\]`)
	wikiBareLinkRe = regexp.MustCompile(`\[((?:https?|mailto):[^\]\s|]+)\]`)
	wikiMentionRe  = regexp.MustCompile(`\[~(?:accountid:)?([^\]]+)\]`)
	wikiBoldRe     = regexp.MustCompile(`(^|[\s(])\*([^*\n]+)\*([\s).,;:!?]|$)`)
	wikiItalicRe   = regexp.MustCompile(`(^|[\s(])_([^_\n]+)_([\s).,;:!?]|$)`)
	wikiMonoRe     = regexp.MustCompile(`\{\{([^}\n]+)\}\}`)
	wikiStrikeRe   = regexp.MustCompile(`(^|\s)-([^-\n]+)-(\s|$)`)
	wikiBulletRe   = regexp.MustCompile(`(?m)^(\*+)\s+`)
	wikiNumberRe   = regexp.MustCompile(`(?m)^(#+)\s+`)
	wikiColorRe    = regexp.MustCompile(`\{color(?::[^}]*)?\}`)
	wikiTableHRe   = regexp.MustCompile(`(?m)^\|\|(.*)\|\|\s*$`)
)

func wikiToMarkdown(s string) string {
	if s == "" || !strings.ContainsAny(s, "*_{[h#|-~") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// I blocchi di codice si mettono da parte: dentro non si converte niente.
	var blocks []string
	keep := func(b string) string {
		blocks = append(blocks, b)
		return "\x00" + string(rune('A'+len(blocks)-1)) + "\x00"
	}
	s = wikiCodeRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := wikiCodeRe.FindStringSubmatch(m)
		return keep("```" + sub[1] + "\n" + strings.Trim(sub[2], "\n") + "\n```")
	})
	s = wikiNoformatRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := wikiNoformatRe.FindStringSubmatch(m)
		return keep("```\n" + strings.Trim(sub[1], "\n") + "\n```")
	})
	s = wikiQuoteRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := wikiQuoteRe.FindStringSubmatch(m)
		lines := strings.Split(strings.Trim(sub[1], "\n"), "\n")
		for i := range lines {
			lines[i] = "> " + lines[i]
		}
		return strings.Join(lines, "\n")
	})
	// Gli elenchi numerati («# voce») prima dei titoli, che in Markdown iniziano con #.
	s = wikiNumberRe.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Repeat("  ", len(strings.TrimSpace(m))-1) + "1. "
	})
	s = wikiHeadingRe.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Repeat("#", int(m[1]-'0')) + " "
	})
	s = wikiMonoRe.ReplaceAllString(s, "`$1`")
	s = wikiLinkRe.ReplaceAllString(s, "[$1]($2)")
	s = wikiBareLinkRe.ReplaceAllString(s, "<$1>")
	s = wikiMentionRe.ReplaceAllString(s, "@$1")
	s = wikiBulletRe.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Repeat("  ", len(strings.TrimSpace(m))-1) + "- "
	})
	s = wikiBoldRe.ReplaceAllString(s, "$1**$2**$3")
	s = wikiItalicRe.ReplaceAllString(s, "$1*$2*$3")
	s = wikiStrikeRe.ReplaceAllString(s, "$1~~$2~~$3")
	s = wikiColorRe.ReplaceAllString(s, "")
	s = wikiTableHRe.ReplaceAllStringFunc(s, func(m string) string {
		cells := strings.Split(strings.Trim(strings.TrimSpace(m), "|"), "||")
		sep := make([]string, len(cells))
		for i := range sep {
			sep[i] = "---"
		}
		return "| " + strings.Join(cells, " | ") + " |\n| " + strings.Join(sep, " | ") + " |"
	})
	for i, b := range blocks {
		s = strings.Replace(s, "\x00"+string(rune('A'+i))+"\x00", b, 1)
	}
	return s
}

// adfText estrae il testo da un documento ADF (descrizioni dell'API v3 di Jira Cloud).
func adfText(v any) string {
	var b strings.Builder
	var walk func(n any, depth int)
	walk = func(n any, depth int) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		t, _ := m["type"].(string)
		if t == "text" {
			txt, _ := m["text"].(string)
			if marks, ok := m["marks"].([]any); ok {
				for _, mk := range marks {
					if mm, ok := mk.(map[string]any); ok {
						switch mm["type"] {
						case "strong":
							txt = "**" + txt + "**"
						case "em":
							txt = "*" + txt + "*"
						case "code":
							txt = "`" + txt + "`"
						case "link":
							if a, ok := mm["attrs"].(map[string]any); ok {
								if href, ok := a["href"].(string); ok {
									txt = "[" + txt + "](" + href + ")"
								}
							}
						}
					}
				}
			}
			b.WriteString(txt)
			return
		}
		switch t {
		case "hardBreak":
			b.WriteString("\n")
			return
		case "mention":
			if a, ok := m["attrs"].(map[string]any); ok {
				if txt, ok := a["text"].(string); ok {
					b.WriteString(txt)
				}
			}
			return
		case "heading":
			level := 1
			if a, ok := m["attrs"].(map[string]any); ok {
				if l, ok := a["level"].(float64); ok {
					level = int(l)
				}
			}
			b.WriteString(strings.Repeat("#", level) + " ")
		case "listItem":
			b.WriteString(strings.Repeat("  ", max(depth-1, 0)) + "- ")
		case "codeBlock":
			b.WriteString("```\n")
		}
		if content, ok := m["content"].([]any); ok {
			for _, c := range content {
				d := depth
				if t == "bulletList" || t == "orderedList" {
					d++
				}
				walk(c, d)
			}
		}
		switch t {
		case "paragraph", "heading", "listItem":
			b.WriteString("\n")
		case "codeBlock":
			b.WriteString("\n```\n")
		}
	}
	walk(v, 0)
	return strings.TrimSpace(b.String())
}
