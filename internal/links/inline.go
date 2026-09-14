package links

import (
	"strings"
	"unicode/utf8"
)

// codeClose returns the end of the first run of exactly n backticks in
// body[from:e], or -1.
func codeClose(body string, from, e, n int) int {
	for j := from; j < e; {
		if body[j] != '`' {
			j++
			continue
		}
		m := run(body, j, e, '`')
		if m == n {
			return j + m
		}
		j += m
	}
	return -1
}

// autolinkEnd returns the end of a URI autolink such as <https://x> that
// starts at i, or 0.
func autolinkEnd(body string, i, e int) int {
	n := schemeLen(body[i+1 : e])
	if n == 0 {
		return 0
	}
	for j := i + 1 + n + 1; j < e; j++ {
		c := body[j]
		if c == '>' {
			return j + 1
		}
		if c <= ' ' || c == '<' || c == 0x7f {
			return 0
		}
	}
	return 0
}

// schemeLen returns the length of a URI scheme at the start of s when a colon
// follows it, and 0 otherwise. A scheme has 2 to 32 characters.
func schemeLen(s string) int {
	if s == "" || !isAlpha(s[0]) {
		return 0
	}
	for n := 1; n < len(s) && n <= 32; n++ {
		c := s[n]
		if c == ':' {
			if n >= 2 {
				return n
			}
			return 0
		}
		if !isAlpha(c) && !(c >= '0' && c <= '9') && c != '+' && c != '.' && c != '-' {
			return 0
		}
	}
	return 0
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// trimRange trims spaces and tabs from both ends of body[s:e].
func trimRange(body string, s, e int) (int, int) {
	for s < e && (body[s] == ' ' || body[s] == '\t') {
		s++
	}
	for e > s && (body[e-1] == ' ' || body[e-1] == '\t') {
		e--
	}
	return s, e
}

// wikilink parses a wikilink whose syntax starts at start, whose content
// starts at from and whose closing "]]" is at close.
func wikilink(body string, start, from, close int, embed bool) (Link, bool) {
	tEnd, lStart := close, -1
	if p := strings.IndexByte(body[from:close], '|'); p >= 0 {
		tEnd = from + p
		lStart = tEnd + 1
		if tEnd > from && body[tEnd-1] == '\\' {
			tEnd--
		}
	}
	if tEnd == from || lStart == close {
		return Link{}, false
	}
	rawEnd := tEnd
	hash := strings.IndexByte(body[from:tEnd], '#')
	if hash >= 0 {
		hash += from
		rawEnd = hash
	}
	ts, te := trimRange(body, from, rawEnd)
	l := Link{Form: FormWikilink, Embed: embed, Start: start, End: close + 2, TargetStart: ts, TargetEnd: te, DestStart: ts, DestEnd: te}
	l.Raw = body[ts:te]
	l.Target = l.Raw
	if hash >= 0 {
		hs, he := trimRange(body, hash+1, tEnd)
		l.Heading = body[hs:he]
	}
	if lStart >= 0 {
		ls, le := trimRange(body, lStart, close)
		l.Label = body[ls:le]
	}
	if l.Raw == "" {
		if hash < 0 {
			return Link{}, false
		}
		l.External = true
	}
	if strings.IndexByte(l.Raw, '/') >= 0 {
		l.Form = FormPathWikilink
	}
	return l, true
}

// skipSpace skips spaces and tabs and at most one line ending.
func skipSpace(body string, i, e int) int {
	for i < e && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	if i < e && body[i] == '\r' {
		i++
		if i < e && body[i] == '\n' {
			i++
		}
	} else if i < e && body[i] == '\n' {
		i++
	} else {
		return i
	}
	for i < e && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	return i
}

// inlineLink parses the destination of an inline link or image whose label
// closes at the ']' at close.
func inlineLink(body string, op opener, close, e int) (Link, bool) {
	j := close + 1
	if j >= e || body[j] != '(' {
		return Link{}, false
	}
	k := skipSpace(body, j+1, e)
	l := Link{Form: FormMarkdownLink, Start: op.pos, DestStart: k}
	labelStart := op.pos + 1
	if op.image {
		l.Form = FormMarkdownImage
		labelStart++
	}
	l.Label = body[labelStart:close]
	var innerS, innerE int
	if k < e && body[k] == '<' {
		m := k + 1
		for m < e {
			c := body[m]
			if c == '\\' && m+1 < e && isPunct(body[m+1]) {
				m += 2
				continue
			}
			if c == '>' {
				break
			}
			if c == '<' || c == '\n' || c == '\r' {
				return Link{}, false
			}
			m++
		}
		if m >= e {
			return Link{}, false
		}
		innerS, innerE = k+1, m
		l.Angle = true
		k = m + 1
	} else {
		m, depth := k, 0
		for m < e {
			c := body[m]
			if c == '\\' && m+1 < e && isPunct(body[m+1]) {
				m += 2
				continue
			}
			if c <= ' ' || c == 0x7f {
				break
			}
			if c == '(' {
				depth++
			} else if c == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
			m++
		}
		if depth != 0 {
			return Link{}, false
		}
		innerS, innerE = k, m
		k = m
	}
	l.DestEnd = k
	t := skipSpace(body, k, e)
	if t > k && t < e && (body[t] == '"' || body[t] == '\'' || body[t] == '(') {
		closer := body[t]
		if closer == '(' {
			closer = ')'
		}
		m := t + 1
		for m < e {
			c := body[m]
			if c == '\\' && m+1 < e && isPunct(body[m+1]) {
				m += 2
				continue
			}
			if c == closer {
				break
			}
			if body[t] == '(' && c == '(' {
				return Link{}, false
			}
			m++
		}
		if m >= e {
			return Link{}, false
		}
		k = skipSpace(body, m+1, e)
	} else {
		k = t
	}
	if k >= e || body[k] != ')' {
		return Link{}, false
	}
	l.End = k + 1

	split, hash := innerE, -1
	for m := innerS; m < innerE; {
		c := body[m]
		if c == '\\' && m+1 < innerE && isPunct(body[m+1]) {
			m += 2
			continue
		}
		if c == '?' && split == innerE {
			split = m
		}
		if c == '#' {
			if split == innerE {
				split = m
			}
			hash = m
			break
		}
		m++
	}
	l.TargetStart, l.TargetEnd = innerS, split
	l.Raw = body[innerS:split]
	l.Target = decodeDest(l.Raw)
	if hash >= 0 {
		l.Heading = decodeDest(body[hash+1 : innerE])
	}
	l.External = l.Raw == "" || strings.HasPrefix(l.Raw, "//") || schemeLen(l.Raw) > 0
	return l, true
}

// decodeDest removes backslash escapes, then decodes %XX sequences. When the
// decoded bytes are not valid UTF-8, the escapes are removed but the percent
// sequences are kept.
func decodeDest(s string) string {
	if strings.IndexByte(s, '\\') >= 0 {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) && isPunct(s[i+1]) {
				i++
			}
			b.WriteByte(s[i])
		}
		s = b.String()
	}
	if strings.IndexByte(s, '%') < 0 {
		return s
	}
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			buf = append(buf, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		buf = append(buf, s[i])
	}
	if !utf8.Valid(buf) {
		return s
	}
	return string(buf)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
