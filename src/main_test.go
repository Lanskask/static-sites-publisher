package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newServer(t *testing.T) (http.Handler, *Store) {
	t.Helper()
	s := &Store{Dir: t.TempDir(), MaxPages: 30, MaxPageBytes: 1000, MaxUserBytes: 5000, MaxFiles: 10}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	a := &Auth{Allow: map[string]bool{"dev:me": true}, Origin: "http://localhost:8080", Dev: "dev:me"}
	return newMux(s, a, "pages.test", 1<<20), s
}

// upload posts files as the browser does: one "file" part and one "path" field per file, in the same order.
func upload(t *testing.T, h http.Handler, name string, kv ...string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("name", name)
	for i := 0; i < len(kv); i += 2 {
		_ = mw.WriteField("path", kv[i])
		fw, _ := mw.CreateFormFile("file", filepath.Base(kv[i]))
		_, _ = fw.Write([]byte(kv[i+1]))
	}
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/api/pages", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUploadFolder(t *testing.T) {
	h, s := newServer(t)
	w := upload(t, h, "blog", "blog/index.html", "<h1>hi</h1>", "blog/css/a.css", "body{}")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "pages/blog/css/a.css")); string(b) != "body{}" {
		t.Fatalf("a.css = %q", b)
	}
}

func TestUploadZip(t *testing.T) {
	h, s := newServer(t)
	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	f, _ := zw.Create("site/index.html")
	_, _ = f.Write([]byte("zipped"))
	_ = zw.Close()
	w := upload(t, h, "z", "site.zip", z.String())
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "pages/z/index.html")); string(b) != "zipped" {
		t.Fatalf("index.html = %q", b)
	}
}

func TestUploadErrors(t *testing.T) {
	h, _ := newServer(t)
	tests := []struct {
		name string
		page string
		kv   []string
		want int
	}{
		{"bad name", "Bad_Name", []string{"index.html", "x"}, 400},
		{"path escape", "ok", []string{"index.html", "x", "../evil", "x"}, 400},
		{"no index", "ok", []string{"a.css", "x", "b.css", "x"}, 400},
		{"too big", "ok", []string{"index.html", string(make([]byte, 1001))}, 413},
		{"no files", "ok", nil, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if w := upload(t, h, tc.page, tc.kv...); w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body)
			}
		})
	}
}

func TestListAndDelete(t *testing.T) {
	h, _ := newServer(t)
	upload(t, h, "blog", "index.html", "x")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/pages", nil))
	var got struct {
		Domain string `json:"domain"`
		Pages  []Meta `json:"pages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Domain != "pages.test" || len(got.Pages) != 1 {
		t.Fatalf("list = %s, %v", w.Body, err)
	}

	r := httptest.NewRequest("DELETE", "/api/pages/blog", nil)
	r.Header.Set("Origin", "http://localhost:8080")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("delete status %d: %s", w.Code, w.Body)
	}
}

func TestSetTagsAPI(t *testing.T) {
	h, _ := newServer(t)
	upload(t, h, "blog", "index.html", "x")
	put := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/pages/blog/tags", bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://localhost:8080")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := put(`["go","learning"]`); w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if w := put(`["bad tag"]`); w.Code != 400 {
		t.Fatalf("bad tag status %d, want 400", w.Code)
	}
	if w := put(`not json`); w.Code != 400 {
		t.Fatalf("bad json status %d, want 400", w.Code)
	}
}

func TestTLSCheck(t *testing.T) {
	h, _ := newServer(t)
	if w := upload(t, h, "demo", "index.html", "<p>hi</p>"); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	for domain, want := range map[string]int{
		"demo.pages.test":   http.StatusOK,
		"nope.pages.test":   http.StatusNotFound, // not published
		"demo.other.test":   http.StatusNotFound, // not our domain
		"pages.test":        http.StatusNotFound,
		"a.demo.pages.test": http.StatusNotFound,
		"admin.pages.test":  http.StatusNotFound, // reserved
		"localhost":         http.StatusOK,       // the admin's own host (Origin), which *.PAGES_DOMAIN may cover
		"":                  http.StatusNotFound,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/tls-check?domain="+domain, nil))
		if w.Code != want {
			t.Errorf("%q: got %d, want %d", domain, w.Code, want)
		}
	}
}
