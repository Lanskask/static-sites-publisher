// Command sites-admin is the admin for static pages: upload a folder, zip or .html file and it is served at
// https://<name>.<PAGES_DOMAIN> by a separate nginx that reads the same directory.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
)

//go:embed static
var static embed.FS

func main() {
	base := strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/")
	store := &Store{
		Dir:          env("SITES_DIR", "./data"),
		MaxPages:     30,
		MaxPageBytes: 20 << 20,
		MaxUserBytes: 300 << 20,
		MaxFiles:     2000,
	}
	if addr := os.Getenv("CLAMD_ADDR"); addr != "" { // optional: host:port of a clamd daemon
		store.Scan = ClamdScanner(addr)
	}
	if err := store.Init(); err != nil {
		log.Fatal(err)
	}
	auth := &Auth{
		Key:       []byte(os.Getenv("SESSION_KEY")),
		Allow:     ParseAllowlist(os.Getenv("ALLOWLIST")),
		Origin:    base,
		Providers: map[string]*Provider{},
		Tokens:    &Tokens{Dir: filepath.Join(store.Dir, "tokens"), MaxPerOwner: 10},
	}
	if strings.HasPrefix(base, "http://localhost") {
		auth.Dev = os.Getenv("DEV_IDENTITY") // never honoured on a public URL
	} else if len(auth.Key) < 32 {
		log.Fatal("SESSION_KEY must be at least 32 bytes")
	}
	add := func(name, idVar, secretVar, userURL string, ep oauth2.Endpoint, scopes ...string) {
		if id := os.Getenv(idVar); id != "" {
			auth.Providers[name] = &Provider{UserURL: userURL, Config: oauth2.Config{
				ClientID: id, ClientSecret: os.Getenv(secretVar), Endpoint: ep, Scopes: scopes,
				RedirectURL: base + "/auth/" + name + "/callback",
			}}
		}
	}
	add("github", "GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "https://api.github.com/user", endpoints.GitHub)
	add("google", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "https://openidconnect.googleapis.com/v1/userinfo",
		endpoints.Google, "openid")

	addr := env("ADDR", ":8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(store, auth, env("PAGES_DOMAIN", "localhost"), 25<<20),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // uploads over slow links
	}
	log.Printf("listening on %s, admin %s", addr, base)
	log.Fatal(srv.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newMux(store *Store, auth *Auth, pagesDomain string, maxUpload int64) http.Handler {
	mux := http.NewServeMux()
	ui, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // the embedded directory is part of the binary
	}
	mux.Handle("GET /", http.FileServerFS(ui))
	mux.HandleFunc("GET /auth/{provider}/start", auth.Start)
	mux.HandleFunc("GET /auth/{provider}/callback", auth.Callback)
	mux.HandleFunc("POST /logout", auth.Logout)
	// Caddy's on-demand TLS asks here before it gets a certificate for <page>.<PAGES_DOMAIN>: only published pages get one.
	// The admin's own host is allowed too: when it is admin.<PAGES_DOMAIN>, Caddy treats it as on-demand as well.
	adminHost := ""
	if u, err := url.Parse(auth.Origin); err == nil {
		adminHost = u.Hostname()
	}
	mux.HandleFunc("GET /tls-check", func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		name, ok := strings.CutSuffix(domain, "."+pagesDomain)
		if domain != adminHost && (!ok || ValidName(name) != nil || !store.Exists(name)) {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /api/providers", func(w http.ResponseWriter, _ *http.Request) {
		names := []string{}
		for n := range auth.Providers {
			names = append(names, n)
		}
		writeJSON(w, names)
	})
	mux.Handle("GET /api/me", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		writeJSON(w, map[string]string{"id": id, "name": displayName(r)})
	}))
	mux.Handle("GET /api/pages", auth.Require(func(w http.ResponseWriter, _ *http.Request, id string) {
		pages, err := store.List(id)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, map[string]any{"domain": pagesDomain, "pages": pages})
	}))
	mux.Handle("POST /api/pages", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			fail(w, err)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		entries, err := formEntries(r.MultipartForm)
		if err != nil {
			fail(w, err)
			return
		}
		m, err := store.Deploy(id, r.FormValue("name"), entries)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, m)
	}))
	mux.Handle("GET /api/tokens", auth.RequireSession(func(w http.ResponseWriter, _ *http.Request, id string) {
		list, err := auth.Tokens.List(id)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, list)
	}))
	mux.Handle("POST /api/tokens", auth.RequireSession(func(w http.ResponseWriter, r *http.Request, id string) {
		var in struct{ Label string }
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) // a missing body just means no label
		label := strings.TrimSpace(in.Label)
		if label == "" {
			label = "token"
		}
		tok, err := auth.Tokens.Create(id, label[:min(len(label), 60)])
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, tok)
	}))
	mux.Handle("DELETE /api/tokens/{id}", auth.RequireSession(func(w http.ResponseWriter, r *http.Request, id string) {
		if err := auth.Tokens.Revoke(id, r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("PUT /api/pages/{name}/tags", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		var tags []string
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&tags); err != nil {
			http.Error(w, "body must be a JSON array of tags", http.StatusBadRequest)
			return
		}
		m, err := store.SetTags(id, r.PathValue("name"), tags)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, m)
	}))
	mux.Handle("DELETE /api/pages/{name}", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		if err := store.Delete(id, r.PathValue("name")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	addEditorRoutes(mux, store, auth, maxUpload)
	return securityHeaders(mux)
}

// formEntries turns the multipart form into upload entries. The browser sends one "path" field per "file" part,
// because Go drops directories from part file names. A single .zip is unpacked.
func formEntries(f *multipart.Form) ([]Entry, error) {
	files, paths := f.File["file"], f.Value["path"]
	if len(files) == 0 || len(files) != len(paths) {
		return nil, errBadForm
	}
	if len(files) == 1 && strings.HasSuffix(strings.ToLower(paths[0]), ".zip") {
		mf, err := files[0].Open()
		if err != nil {
			return nil, err
		}
		ra, ok := mf.(io.ReaderAt)
		if !ok {
			return nil, errBadForm
		}
		return ZipEntries(ra, files[0].Size) // mf stays open until the request ends; RemoveAll deletes the temp file
	}
	es := make([]Entry, len(files))
	for i, fh := range files {
		es[i] = Entry{Path: paths[i], Open: func() (io.ReadCloser, error) { return fh.Open() }}
	}
	return es, nil
}

var errBadForm = errors.New("upload form needs one path per file")

func fail(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	code := http.StatusInternalServerError
	switch {
	case errors.As(err, &tooLarge), errors.Is(err, ErrTooBig):
		code, err = http.StatusRequestEntityTooLarge, ErrTooBig
	case errors.Is(err, ErrRejected):
		code = http.StatusUnprocessableEntity
	case errors.Is(err, ErrQuota), errors.Is(err, ErrTaken):
		code = http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ErrConflict):
		code = http.StatusPreconditionFailed
	case errors.Is(err, ErrBadName), errors.Is(err, ErrBadPath), errors.Is(err, ErrNoIndex),
		errors.Is(err, ErrTooMany), errors.Is(err, errBadForm), errors.Is(err, multipart.ErrMessageTooLarge),
		errors.Is(err, ErrBadZip), errors.Is(err, ErrBadTag), errors.Is(err, ErrBadFavicon):
		code = http.StatusBadRequest
	default:
		log.Printf("error: %v", err)
		err = errors.New("internal error")
	}
	http.Error(w, err.Error(), code)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}
