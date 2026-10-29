package httpserver

import (
	"net/http"
)

// responseWriter records the status code and body size of a response.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

// wrap returns w if it is already a responseWriter, so stacked middleware
// share one.
func wrap(w http.ResponseWriter) *responseWriter {
	if rw, ok := w.(*responseWriter); ok {
		return rw
	}
	return &responseWriter{ResponseWriter: w}
}

func (w *responseWriter) WriteHeader(status int) {
	// 1xx responses are informational and followed by the real one.
	if w.status == 0 && status >= http.StatusOK {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach Flush, Hijack and deadlines on the
// underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// statusCode returns 200 for a handler that wrote nothing, as net/http does.
func (w *responseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
