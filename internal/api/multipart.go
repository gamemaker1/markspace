package api

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// MultipartMemory is how much of a buffered multipart form stays in
	// memory before parts spill to disk.
	MultipartMemory = 32 << 20
	// multipartOverhead is the allowance for boundaries and form fields on
	// top of the upload limit.
	multipartOverhead = 1 << 20
	// maxFieldBytes caps one text field of a streamed multipart body.
	maxFieldBytes = 4 << 10
)

// LimitUpload caps a streamed upload body at max bytes plus multipart
// overhead.
func LimitUpload(w http.ResponseWriter, r *http.Request, max int64) {
	r.Body = http.MaxBytesReader(w, r.Body, max+multipartOverhead)
}

// UploadParts reads a streamed multipart body. It collects the text fields
// named in allowed that come before the part named "file", and returns that
// part unread. Fields after the file part are never seen. Other parts are
// skipped.
func UploadParts(r *http.Request, allowed ...string) (map[string]string, *multipart.Part, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, core.Invalid("file", "a multipart body is required")
	}
	fields := map[string]string{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, nil, core.Invalid("file", "no file part named file")
		}
		if err != nil {
			return nil, nil, bodyErr(err, "file", "could not read the multipart body")
		}
		name := part.FormName()
		if name == "file" {
			return fields, part, nil
		}
		keep := false
		for _, a := range allowed {
			if a == name {
				keep = true
				break
			}
		}
		if !keep {
			if _, err := io.Copy(io.Discard, part); err != nil {
				return nil, nil, bodyErr(err, "file", "could not read the multipart body")
			}
			continue
		}
		b, err := io.ReadAll(io.LimitReader(part, maxFieldBytes+1))
		if err != nil {
			return nil, nil, bodyErr(err, name, "could not read the field")
		}
		if len(b) > maxFieldBytes {
			return nil, nil, core.Invalid(name, "the field is too long")
		}
		fields[name] = strings.TrimSpace(string(b))
	}
}

// bodyErr maps a failed body read to ErrTooLarge when the size limit caused
// it, and to a validation error otherwise.
func bodyErr(err error, field, msg string) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return core.ErrTooLarge
	}
	return core.Invalid(field, msg)
}

// UploadErr maps a backend error from a streamed upload. A read that hit the
// body limit answers 413 even when the backend did not map it.
func UploadErr(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return core.ErrTooLarge
	}
	// A body that ends early or breaks the multipart framing is a client
	// error, not a server fault.
	if StatusFor(err) == 0 && (errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "multipart")) {
		return core.Invalid("file", "the multipart body is malformed")
	}
	return err
}

// PartName returns the base name of an uploaded part.
func PartName(part *multipart.Part) string {
	name := strings.ReplaceAll(part.FileName(), `\`, "/")
	name = path.Base(name)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// FirstFile returns the first part stored under key of a buffered form.
func FirstFile(form *multipart.Form, key string) (*multipart.FileHeader, error) {
	if form == nil {
		return nil, core.Invalid(key, "a multipart body is required")
	}
	if headers := form.File[key]; len(headers) > 0 {
		return headers[0], nil
	}
	return nil, core.Invalid(key, "no file part named "+key)
}

// SafeFilename strips directory separators and quotes from a file name so it
// can appear in a Content-Disposition header.
func SafeFilename(name string) string {
	name = strings.ReplaceAll(path.Base(name), `"`, "")
	if name == "" || name == "." || name == "/" {
		return "download"
	}
	return name
}
