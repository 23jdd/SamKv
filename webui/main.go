package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	address := flag.String("addr", "127.0.0.1", "Webui listen address")
	port := flag.String("port", "9998", "Webui port")
	api := flag.String("api", "http://127.0.0.1:9999", "SamKV API origin")
	static := flag.String("static", "", "Frontend static directory")
	flag.Parse()

	staticDir, err := resolveStaticDir(*static)
	if err != nil {
		log.Fatal(err)
	}
	apiURL, err := url.Parse(*api)
	if err != nil {
		log.Fatalf("invalid api origin: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", apiProxy(apiURL))
	if logo := firstExistingPath("logo.png", filepath.Join("..", "logo.png")); logo != "" {
		mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, logo)
		})
	}
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	addr := fmt.Sprintf("%s:%s", *address, *port)
	log.Printf("SamKV WebUI listening on http://%s", addr)
	log.Printf("Proxying /api/* to %s", apiURL.String())
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func apiProxy(target *url.URL) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.URL.Path = strings.TrimPrefix(request.URL.Path, "/api")
		if request.URL.Path == "" {
			request.URL.Path = "/"
		}
		request.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("SamKV API unavailable: %s", err.Error()),
		})
	}
	return proxy
}

func resolveStaticDir(configured string) (string, error) {
	if configured != "" {
		if isDirectory(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("frontend static directory %q does not exist", configured)
	}
	static := firstExistingDir("webui/frontend", "frontend")
	if static == "" {
		return "", fmt.Errorf("frontend static directory not found")
	}
	return static, nil
}

func firstExistingDir(paths ...string) string {
	for _, path := range paths {
		if isDirectory(path) {
			return path
		}
	}
	return ""
}

func firstExistingPath(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
