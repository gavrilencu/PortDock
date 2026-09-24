package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// JSON responses of the API are compressed when the browser accepts it; a
// list of a hundred applications or the topology are 150–200 KB of text that
// shrink to a tenth. Streams (events, logs, terminals) are left alone.

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

func gzipEligible(r *http.Request) bool {
	if r.Method != http.MethodGet || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return false
	}
	p := r.URL.Path
	if !strings.HasPrefix(p, "/api/") {
		return false
	}
	for _, s := range []string{"/stream", "/logs", "/terminal", "/events", "/reports", ".zip", "/download", "/export"} {
		if strings.Contains(p, s) {
			return false
		}
	}
	return true
}

// gzipWriter compresses the body once the handler sets a JSON content type.
type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
	on      bool
}

func (g *gzipWriter) decide() {
	if g.decided {
		return
	}
	g.decided = true
	h := g.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "application/json") || h.Get("Content-Encoding") != "" {
		return
	}
	g.on = true
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	h.Del("Content-Length")
	g.gz = gzipPool.Get().(*gzip.Writer)
	g.gz.Reset(g.ResponseWriter)
}

func (g *gzipWriter) WriteHeader(code int) {
	g.decide()
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	g.decide()
	if g.on {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) Flush() {
	if g.on {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) close() {
	if g.on {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
	}
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gzipEligible(r) {
			next.ServeHTTP(w, r)
			return
		}
		g := &gzipWriter{ResponseWriter: w}
		defer g.close()
		next.ServeHTTP(g, r)
	})
}
