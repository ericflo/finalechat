package messenger

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Messenger renders a small markdown dialect of its own: *bold*, _italic_,
// ~strike~, `code` and ``` blocks. Headings, links, list markers and
// **double** emphasis show up as literal punctuation, so Markdown rewrites an
// agent's CommonMark into that dialect and plain text.

var (
	headingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	bulletRe  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	quoteRe   = regexp.MustCompile(`^\s{0,3}>\s?(.*)$`)
	ruleRe    = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	imageRe   = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	linkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	strongRe  = regexp.MustCompile(`\*\*(\S(?:.*?\S)?)\*\*|__(\S(?:.*?\S)?)__`)
	emRe      = regexp.MustCompile(`(^|[^\w*])\*(\S(?:[^*]*?\S)?)\*($|[^\w*])`)
	strikeRe  = regexp.MustCompile(`~~(\S(?:.*?\S)?)~~`)
)

// Markdown converts CommonMark to what Messenger displays well. Fenced code
// blocks and inline code pass through untouched.
func Markdown(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	fenced := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			out = append(out, strings.TrimSpace(line))
			continue
		}
		if fenced {
			out = append(out, line)
			continue
		}
		switch {
		case ruleRe.MatchString(line):
			line = "———"
		case headingRe.MatchString(line):
			text := inline(headingRe.FindStringSubmatch(line)[1])
			// Bold inside a bold heading would close it early.
			line = "*" + strings.ReplaceAll(text, "*", "") + "*"
		case bulletRe.MatchString(line):
			m := bulletRe.FindStringSubmatch(line)
			line = m[1] + "• " + inline(m[2])
		case quoteRe.MatchString(line):
			line = "│ " + inline(quoteRe.FindStringSubmatch(line)[1])
		default:
			line = inline(line)
		}
		out = append(out, line)
	}
	return strings.TrimSpace(collapseBlankLines(strings.Join(out, "\n")))
}

// inline rewrites emphasis and links outside `code` spans.
func inline(s string) string {
	parts := strings.Split(s, "`")
	for i := 0; i < len(parts); i += 2 {
		// Even indexes are outside code spans (an unmatched backtick leaves
		// the tail as prose, which is what a reader would see anyway).
		p := parts[i]
		p = imageRe.ReplaceAllString(p, "[image: $1]")
		p = linkRe.ReplaceAllStringFunc(p, func(m string) string {
			sm := linkRe.FindStringSubmatch(m)
			if sm[1] == sm[2] || strings.TrimPrefix(strings.TrimPrefix(sm[2], "https://"), "http://") == sm[1] {
				return sm[2]
			}
			return sm[1] + " (" + sm[2] + ")"
		})
		// Single-star emphasis becomes _italic_ first, then **strong**
		// becomes Messenger's *bold*.
		p = strongRe.ReplaceAllString(p, "\x00$1$2\x00")
		for emRe.MatchString(p) {
			p = emRe.ReplaceAllString(p, "${1}_${2}_${3}")
		}
		p = strings.ReplaceAll(p, "\x00", "*")
		p = strikeRe.ReplaceAllString(p, "~$1~")
		parts[i] = p
	}
	return strings.Join(parts, "`")
}

func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// Chunk splits text into pieces of at most max characters, preferring
// paragraph, then line, then word boundaries. A code block cut in two is
// closed at the end of one piece and reopened at the start of the next.
func Chunk(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if utf8.RuneCountInString(text) <= max {
		return []string{text}
	}
	// Room for a fence closed at the end and reopened at the start.
	budget := max - 8
	var pieces []string
	rest := text
	for rest != "" {
		if utf8.RuneCountInString(rest) <= budget {
			pieces = append(pieces, rest)
			break
		}
		cut := cutPoint(rest, budget)
		pieces = append(pieces, strings.TrimRight(rest[:cut], " \n"))
		rest = strings.TrimLeft(rest[cut:], " \n")
	}
	open := false
	for i, p := range pieces {
		if open {
			p = "```\n" + p
		}
		if strings.Count(p, "```")%2 == 1 {
			p += "\n```"
			open = true
		} else {
			open = false
		}
		pieces[i] = p
	}
	return pieces
}

// cutPoint returns a byte offset in s at most budget runes in, on the best
// boundary available.
func cutPoint(s string, budget int) int {
	limit := len(s)
	n := 0
	for i := range s {
		if n == budget {
			limit = i
			break
		}
		n++
	}
	window := s[:limit]
	for _, sep := range []string{"\n\n", "\n", " "} {
		if i := strings.LastIndex(window, sep); i > limit/3 {
			return i + len(sep)
		}
	}
	return limit
}
