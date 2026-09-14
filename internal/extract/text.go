package extract

import (
	"bytes"
	"fmt"
	"mime"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// textMediaTypes lists the non text/* media types that hold UTF-8 text.
var textMediaTypes = map[string]bool{
	"application/json":     true,
	"application/x-ndjson": true,
	"application/yaml":     true,
	"application/x-yaml":   true,
	"text/yaml":            true,
	"text/x-yaml":          true,
	"text/csv":             true,
	"text/markdown":        true,
	"text/x-markdown":      true,
}

// textExtensions lists the file extensions of text files whose media type was
// stored as something generic, such as application/octet-stream.
var textExtensions = map[string]bool{
	".txt":      true,
	".text":     true,
	".md":       true,
	".markdown": true,
	".json":     true,
	".yaml":     true,
	".yml":      true,
	".csv":      true,
}

// IsText reports whether a file holds text, by its media type or, failing
// that, by the extension of its name. It accepts text/*, JSON, YAML, CSV and
// markdown.
func IsText(mediaType, name string) bool {
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	if parsed, _, err := mime.ParseMediaType(mt); err == nil {
		mt = parsed
	} else if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	if strings.HasPrefix(mt, "text/") || textMediaTypes[mt] || strings.HasSuffix(mt, "+json") {
		return true
	}
	return textExtensions[strings.ToLower(path.Ext(name))]
}

// DecodeText returns data as a string after checking that it is UTF-8 text.
// A leading byte order mark is removed. An incomplete rune at the very end,
// as left by a read that stopped at a byte limit, is dropped. Data with a NUL
// byte or any other invalid UTF-8 fails with core.ErrUnsupported.
func DecodeText(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	data = trimPartialRune(data)
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("extract: file holds binary data: %w", core.ErrUnsupported)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("extract: file is not UTF-8 text: %w", core.ErrUnsupported)
	}
	return string(data), nil
}

// trimPartialRune drops an incomplete UTF-8 sequence from the end of data.
func trimPartialRune(data []byte) []byte {
	for back := 1; back <= utf8.UTFMax && back <= len(data); back++ {
		start := len(data) - back
		if !utf8.RuneStart(data[start]) {
			continue
		}
		if !utf8.FullRune(data[start:]) {
			return data[:start]
		}
		return data
	}
	return data
}
