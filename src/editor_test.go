package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDraftSaveListDelete(t *testing.T) {
	s := newStore(t)
	if _, err := s.SaveDraft("u1", Draft{Name: "Bad_Name", HTML: "x"}); !errors.Is(err, ErrBadName) {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := s.SaveDraft("u1", Draft{Name: "big", HTML: strings.Repeat("x", 101)}); !errors.Is(err, ErrTooBig) {
		t.Fatalf("too big: %v", err)
	}
	if _, err := s.SaveDraft("u1", Draft{Name: "blog", HTML: "<p>v1</p>"}); err != nil {
		t.Fatal(err)
	}
	// drafts are per owner: u2 neither sees u1's draft nor collides with it
	if _, err := s.SaveDraft("u2", Draft{Name: "blog", HTML: "<p>other</p>"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.Drafts("u1")
	if err != nil || len(list) != 1 || list[0].Name != "blog" || list[0].HTML != "" || list[0].Saved.IsZero() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	d, err := s.GetDraft("u1", "blog")
	if err != nil || d.HTML != "<p>v1</p>" {
		t.Fatalf("get = %+v, %v", d, err)
	}
	if _, err := s.GetDraft("u1", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing draft: %v", err)
	}
	if err := s.DeleteDraft("u1", "blog"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Drafts("u1"); len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}
	if d, _ := s.GetDraft("u2", "blog"); d.HTML != "<p>other</p>" {
		t.Fatalf("u2 draft = %+v", d)
	}
	// a draft is not published: the page does not exist, so it gets no certificate and no URL
	if s.Exists("blog") {
		t.Fatal("draft made the page exist")
	}
}

func TestPublishNewPage(t *testing.T) {
	s := newStore(t)
	if _, err := s.SaveDraft("u1", Draft{Name: "hello", HTML: "<h1>hi</h1>"}); err != nil {
		t.Fatal(err)
	}
	m, err := s.Publish("u1", "hello", false)
	if err != nil || m.Name != "hello" {
		t.Fatalf("publish = %+v, %v", m, err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "pages/hello/index.html")); string(b) != "<h1>hi</h1>" {
		t.Fatalf("index.html = %q", b)
	}
	if _, err := s.GetDraft("u1", "hello"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft kept after publish: %v", err)
	}
}

func TestPublishEditKeepsOtherFilesAndTags(t *testing.T) {
	s := newStore(t)
	old, err := s.Deploy("u1", "blog", files("index.html", "old", "css/a.css", "body{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTags("u1", "blog", []string{"go"}); err != nil {
		t.Fatal(err)
	}
	src, m, err := s.Source("u1", "blog")
	if err != nil || string(src) != "old" {
		t.Fatalf("source = %q, %v", src, err)
	}
	if _, err := s.SaveDraft("u1", Draft{Name: "blog", HTML: "new", Base: m.Updated}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "pages/blog/index.html")); string(b) != "old" {
		t.Fatal("saving a draft changed the live page")
	}
	time.Sleep(time.Millisecond)
	got, err := s.Publish("u1", "blog", false)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "pages/blog/index.html")); string(b) != "new" {
		t.Fatalf("index.html = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "pages/blog/css/a.css")); string(b) != "body{}" {
		t.Fatalf("a.css = %q", b)
	}
	if len(got.Tags) != 1 || !got.Created.Equal(old.Created) || !got.Updated.After(old.Updated) {
		t.Fatalf("meta = %+v", got)
	}
}

func TestPublishConflict(t *testing.T) {
	s := newStore(t)
	m, _ := s.Deploy("u1", "blog", files("index.html", "v1"))
	if _, err := s.SaveDraft("u1", Draft{Name: "blog", HTML: "mine", Base: m.Updated}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := s.Deploy("u1", "blog", files("index.html", "uploaded meanwhile")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("u1", "blog", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale draft published: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "pages/blog/index.html")); string(b) != "uploaded meanwhile" {
		t.Fatalf("index.html = %q", b)
	}
	if _, err := s.Publish("u1", "blog", true); err != nil {
		t.Fatalf("forced publish: %v", err)
	}
}

func TestPublishOthersPageAndScanner(t *testing.T) {
	s := newStore(t)
	if _, err := s.Deploy("u1", "blog", files("index.html", "x")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDraft("u2", Draft{Name: "blog", HTML: "mine"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("u2", "blog", false); !errors.Is(err, ErrTaken) {
		t.Fatalf("publish over another owner's page: %v", err)
	}
	if _, _, err := s.Source("u2", "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source of another owner's page: %v", err)
	}
	if _, err := s.SaveDraft("u1", Draft{Name: "phish", HTML: `<form><input type="password"></form>`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("u1", "phish", false); !errors.Is(err, ErrRejected) {
		t.Fatalf("scanner skipped: %v", err)
	}
}

func TestEditorAPI(t *testing.T) {
	h, _ := newServer(t)
	upload(t, h, "blog", "index.html", "<p>v1</p>")
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://localhost:8080")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := do("GET", "/api/pages/blog/source", "")
	if w.Code != 200 || w.Body.String() != "<p>v1</p>" {
		t.Fatalf("source: %d %s", w.Code, w.Body)
	}
	hd := w.Header()
	if !strings.HasPrefix(hd.Get("Content-Type"), "text/plain") || !strings.Contains(hd.Get("Content-Security-Policy"), "sandbox") || hd.Get("X-Page-Updated") == "" {
		t.Fatalf("source headers: %v", hd)
	}
	base := hd.Get("X-Page-Updated")

	body, _ := json.Marshal(map[string]string{"html": "<p>v2</p>", "base": base})
	if w := do("PUT", "/api/drafts/blog", string(body)); w.Code != 200 {
		t.Fatalf("save draft: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/drafts", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"blog"`) {
		t.Fatalf("list drafts: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/drafts/blog", ""); !strings.Contains(w.Body.String(), "v2") {
		t.Fatalf("get draft: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/api/drafts/blog/publish", ""); w.Code != 200 {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/pages/blog/source", ""); w.Body.String() != "<p>v2</p>" {
		t.Fatalf("after publish: %s", w.Body)
	}

	// a stale draft is refused with 412 until forced
	body, _ = json.Marshal(map[string]string{"html": "<p>v3</p>", "base": base})
	do("PUT", "/api/drafts/blog", string(body))
	if w := do("POST", "/api/drafts/blog/publish", ""); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale publish: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/api/drafts/blog/publish?force=1", ""); w.Code != 200 {
		t.Fatalf("forced publish: %d %s", w.Code, w.Body)
	}
	if w := do("DELETE", "/api/drafts/blog", ""); w.Code != 404 {
		t.Fatalf("draft should be gone after publish: %d", w.Code)
	}

	body, _ = json.Marshal(map[string]string{"html": "<head><title>t</title></head>", "href": "data:image/svg+xml,<svg/>"})
	if w := do("POST", "/api/favicon", string(body)); w.Code != 200 || !strings.Contains(w.Body.String(), `rel=\"icon\"`) {
		t.Fatalf("favicon: %d %s", w.Code, w.Body)
	}
	body, _ = json.Marshal(map[string]string{"html": "x", "href": "javascript:alert(1)"})
	if w := do("POST", "/api/favicon", string(body)); w.Code != 400 {
		t.Fatalf("bad favicon href: %d %s", w.Code, w.Body)
	}

	// writes need the admin's origin, like every other write
	r := httptest.NewRequest("PUT", "/api/drafts/blog", bytes.NewBufferString(`{"html":"x"}`))
	r.Header.Set("Origin", "null")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin draft save: %d", w.Code)
	}
}

func TestPreviewIsSandboxed(t *testing.T) {
	h, _ := newServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/preview", nil))
	csp := w.Header().Get("Content-Security-Policy")
	if w.Code != 200 || !strings.Contains(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") ||
		!strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("preview: %d, CSP %q", w.Code, csp)
	}
}
