package store

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
)

// defaultMediaType is used when nothing better can be detected.
const defaultMediaType = "application/octet-stream"

// copyWithSniff copies src into dst and keeps the first 512 bytes.
func copyWithSniff(dst io.Writer, src io.Reader, sniff *[]byte) (int64, error) {
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if len(*sniff) < 512 {
				room := 512 - len(*sniff)
				if room > n {
					room = n
				}
				*sniff = append(*sniff, buf[:room]...)
			}
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// DetectMediaType returns the media type of a file. It prefers the
// extension when it is known and otherwise sniffs the leading bytes.
func DetectMediaType(filename string, head []byte) string {
	base := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	if ext := path.Ext(base); ext != "" {
		if byExt := mime.TypeByExtension(strings.ToLower(ext)); byExt != "" {
			return byExt
		}
	}
	if len(head) == 0 {
		return defaultMediaType
	}
	return http.DetectContentType(head)
}
