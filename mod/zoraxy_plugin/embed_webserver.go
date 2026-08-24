package zoraxy_plugin

import (
	"embed"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PluginUiRouter struct {
	targetFS       *embed.FS
	targetFSPrefix string
	handlerPrefix  string
	devRoot        string
}

func NewPluginEmbedUIRouter(target *embed.FS, targetPrefix, handlerPrefix string) *PluginUiRouter {
	if !strings.HasPrefix(targetPrefix, "/") {
		targetPrefix = "/" + targetPrefix
	}
	targetPrefix = strings.TrimSuffix(targetPrefix, "/")
	if !strings.HasPrefix(handlerPrefix, "/") {
		handlerPrefix = "/" + handlerPrefix
	}
	handlerPrefix = strings.TrimSuffix(handlerPrefix, "/")
	return &PluginUiRouter{
		targetFS:       target,
		targetFSPrefix: targetPrefix,
		handlerPrefix:  handlerPrefix,
	}
}

// SetDevWebRoot serves assets from disk while retaining the same CSRF injection
// behavior as the embedded production router.
func (p *PluginUiRouter) SetDevWebRoot(root string) {
	p.devRoot = root
}

func (p *PluginUiRouter) csrfToken(r *http.Request) string {
	token := r.Header.Get("X-Zoraxy-Csrf")
	if token == "" {
		return "missing-csrf-token"
	}
	return token
}

func (p *PluginUiRouter) serveDev(w http.ResponseWriter, r *http.Request) bool {
	if p.devRoot == "" {
		return false
	}
	st, err := os.Stat(p.devRoot)
	if err != nil || !st.IsDir() {
		return false
	}

	rel := strings.TrimPrefix(r.URL.Path, "/")
	if rel == "" {
		rel = "index.html"
	}
	// Avoid path traversal and keep all files inside devRoot.
	rel = filepath.Clean(rel)
	if rel == "." || strings.HasPrefix(rel, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return true
	}
	target := filepath.Join(p.devRoot, rel)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		target = filepath.Join(target, "index.html")
	}
	if strings.HasSuffix(strings.ToLower(target), ".html") {
		body, err := os.ReadFile(target)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		rendered := strings.ReplaceAll(string(body), "{{.csrfToken}}", p.csrfToken(r))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(rendered))
		return true
	}
	http.FileServer(http.Dir(p.devRoot)).ServeHTTP(w, r)
	return true
}

func (p *PluginUiRouter) populateCSRFToken(r *http.Request, fsHandler http.Handler) http.Handler {
	csrfToken := p.csrfToken(r)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, ".html") {
			targetFilePath := strings.TrimPrefix(req.URL.Path, "/")
			targetFilePath = p.targetFSPrefix + "/" + targetFilePath
			targetFilePath = strings.TrimPrefix(targetFilePath, "/")
			targetFileContent, err := fs.ReadFile(*p.targetFS, targetFilePath)
			if err != nil {
				http.Error(w, "File not found", http.StatusNotFound)
				return
			}
			body := strings.ReplaceAll(string(targetFileContent), "{{.csrfToken}}", csrfToken)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(body))
			return
		}
		if strings.HasSuffix(req.URL.Path, "/") {
			indexFilePath := strings.TrimPrefix(req.URL.Path, "/") + "index.html"
			indexFilePath = p.targetFSPrefix + "/" + indexFilePath
			indexFilePath = strings.TrimPrefix(indexFilePath, "/")
			if indexFileContent, err := fs.ReadFile(*p.targetFS, indexFilePath); err == nil {
				body := strings.ReplaceAll(string(indexFileContent), "{{.csrfToken}}", csrfToken)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(body))
				return
			}
		}
		fsHandler.ServeHTTP(w, req)
	})
}

func (p *PluginUiRouter) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rewrittenURL := r.RequestURI
		rewrittenURL = strings.TrimPrefix(rewrittenURL, p.handlerPrefix)
		rewrittenURL = strings.ReplaceAll(rewrittenURL, "//", "/")
		parsed, err := url.Parse(rewrittenURL)
		if err != nil {
			http.Error(w, "invalid URL", http.StatusBadRequest)
			return
		}
		r.URL = parsed
		r.RequestURI = rewrittenURL

		if p.serveDev(w, r) {
			return
		}

		subFS, err := fs.Sub(*p.targetFS, strings.TrimPrefix(p.targetFSPrefix, "/"))
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		p.populateCSRFToken(r, http.FileServer(http.FS(subFS))).ServeHTTP(w, r)
	})
}

func (p *PluginUiRouter) RegisterTerminateHandler(fn func(), mux *http.ServeMux) {
	if mux == nil {
		mux = http.DefaultServeMux
	}
	endpoint := p.handlerPrefix + "/term"
	if p.handlerPrefix == "" || p.handlerPrefix == "/" {
		endpoint = "/term"
	}
	mux.HandleFunc(endpoint, func(w http.ResponseWriter, r *http.Request) {
		if fn != nil {
			fn()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		go func() {
			time.Sleep(100 * time.Millisecond)
			os.Exit(0)
		}()
	})
}
