package httpapi

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
)

type frontend struct {
	root     *os.Root
	handler  http.Handler
	once     sync.Once
	closeErr error
}

// WithFrontend serves an explicitly built directory using a filesystem root that prevents escapes.
func WithFrontend(directory string) Option {
	return func(h *Handler) error {
		if directory == "" {
			return nil
		}
		root, err := os.OpenRoot(directory)
		if err != nil {
			return errors.New("frontend directory is unavailable")
		}
		info, err := fs.Stat(root.FS(), "index.html")
		if err != nil || !info.Mode().IsRegular() {
			return errors.Join(errors.New("frontend must contain built index.html"), root.Close())
		}
		h.frontend = &frontend{root: root, handler: http.FileServerFS(root.FS())}
		return nil
	}
}

// Close releases the optional static directory after the owning server stops accepting requests.
func (h *Handler) Close() error {
	if h.frontend == nil {
		return nil
	}
	h.frontend.once.Do(func() { h.frontend.closeErr = h.frontend.root.Close() })
	return h.frontend.closeErr
}

func (f *frontend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, part := range strings.Split(r.URL.Path, "/") {
		if strings.HasPrefix(part, ".") {
			http.NotFound(w, r)
			return
		}
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	info, err := fs.Stat(f.root.FS(), name)
	if err == nil && info.Mode().IsRegular() {
		f.handler.ServeHTTP(w, r)
		return
	}
	if name != "" && name != "." && (path.Ext(name) != "" || strings.HasPrefix(name, "assets/")) {
		http.NotFound(w, r)
		return
	}
	index, err := f.root.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer index.Close()
	stat, err := index.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", stat.ModTime(), index)
}
