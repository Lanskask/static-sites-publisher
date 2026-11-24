package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testOrigin = "https://admin.test"

// tokenServer is a server with real sessions (no Dev identity) and two users, only "github:1" allowlisted.
func tokenServer(t *testing.T) (http.Handler, *Auth, *Tokens) {
	t.Helper()
	s := &Store{Dir: t.TempDir(), MaxPages: 30, MaxPageBytes: 1000, MaxUserBytes: 5000, MaxFiles: 10}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	tk := &Tokens{Dir: filepath.Join(s.Dir, "tokens"), MaxPerOwner: 3}
	if err := os.MkdirAll(tk.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &Auth{Key: []byte("k"), Allow: map[string]bool{"github:1": true}, Origin: testOrigin, Tokens: tk}
	return newMux(s, a, "pages.test", 1<<20), a, tk
}

func do(h http.Handler, method, url, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func session(a *Auth, id string) map[string]string {
	return map[string]string{"Cookie": sessionCookie + "=" + a.Sign(id, time.Now().Add(time.Hour)), "Origin": testOrigin}
}

func newToken(t *testing.T, h http.Handler, a *Auth, id string) string {
	t.Helper()
	w := do(h, "POST", "/api/tokens", `{"label":"claude"}`, session(a, id))
	if w.Code != 200 {
		t.Fatalf("create token: %d %s", w.Code, w.Body)
	}
	var out struct{ Token string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !strings.HasPrefix(out.Token, "nxs_") {
		t.Fatalf("bad token response: %s", w.Body)
	}
	return out.Token
}

func bearerUpload(h http.Handler, token, name, html string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("name", name)
	_ = mw.WriteField("path", "index.html")
	fw, _ := mw.CreateFormFile("file", "index.html")
	_, _ = fw.Write([]byte(html))
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/api/pages", &body) // no Origin: a script is not a browser
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTokenDeployListDelete(t *testing.T) {
	h, a, _ := tokenServer(t)
	tok := newToken(t, h, a, "github:1")
	bearer := map[string]string{"Authorization": "Bearer " + tok}

	if w := bearerUpload(h, tok, "bot", "<p>hi"); w.Code != 200 {
		t.Fatalf("deploy: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/pages", "", bearer); w.Code != 200 || !strings.Contains(w.Body.String(), `"bot"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := do(h, "DELETE", "/api/pages/bot", "", bearer); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
}

func TestTokenRefused(t *testing.T) {
	h, a, tk := tokenServer(t)
	good := newToken(t, h, a, "github:1")
	if w := do(h, "GET", "/api/pages", "", map[string]string{"Authorization": "Bearer nxs_wrong"}); w.Code != 401 {
		t.Fatalf("unknown token: %d", w.Code)
	}
	// a token of a user who left the allowlist stops working at once
	other, err := tk.Create("github:2", "x")
	if err != nil {
		t.Fatal(err)
	}
	if w := do(h, "GET", "/api/pages", "", map[string]string{"Authorization": "Bearer " + other.Token}); w.Code != 403 {
		t.Fatalf("non-allowlisted token: %d", w.Code)
	}
	// a token cannot mint or list tokens: only a browser session can
	bearer := map[string]string{"Authorization": "Bearer " + good, "Origin": testOrigin}
	if w := do(h, "POST", "/api/tokens", `{"label":"x"}`, bearer); w.Code != 401 {
		t.Fatalf("token minted a token: %d", w.Code)
	}
	if w := do(h, "GET", "/api/tokens", "", bearer); w.Code != 401 {
		t.Fatalf("token listed tokens: %d", w.Code)
	}
}

func TestTokenStoredHashedAndRevocable(t *testing.T) {
	h, a, tk := tokenServer(t)
	tok := newToken(t, h, a, "github:1")
	ents, _ := os.ReadDir(tk.Dir)
	if len(ents) != 1 {
		t.Fatalf("want 1 token file, got %d", len(ents))
	}
	raw, _ := os.ReadFile(filepath.Join(tk.Dir, ents[0].Name()))
	if strings.Contains(string(raw), tok) || strings.Contains(ents[0].Name(), tok) {
		t.Fatal("raw token found on disk")
	}

	w := do(h, "GET", "/api/tokens", "", session(a, "github:1"))
	var list []struct{ ID, Label string }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Label != "claude" {
		t.Fatalf("list tokens: %s", w.Body)
	}
	if strings.Contains(w.Body.String(), tok) {
		t.Fatal("list leaks the token")
	}
	if w := do(h, "DELETE", "/api/tokens/"+list[0].ID, "", session(a, "github:1")); w.Code != 204 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if w := do(h, "GET", "/api/pages", "", map[string]string{"Authorization": "Bearer " + tok}); w.Code != 401 {
		t.Fatalf("revoked token still works: %d", w.Code)
	}
}

func TestTokenLimitAndOwnership(t *testing.T) {
	h, a, tk := tokenServer(t)
	for range 3 {
		newToken(t, h, a, "github:1")
	}
	if w := do(h, "POST", "/api/tokens", `{"label":"4th"}`, session(a, "github:1")); w.Code != 403 {
		t.Fatalf("4th token: %d", w.Code)
	}
	// another owner cannot revoke it
	list, _ := tk.List("github:1")
	if err := tk.Revoke("github:2", list[0].ID); err == nil {
		t.Fatal("foreign revoke succeeded")
	}
}

func TestTokenCreateNeedsOrigin(t *testing.T) {
	h, a, _ := tokenServer(t)
	hdr := session(a, "github:1")
	delete(hdr, "Origin")
	if w := do(h, "POST", "/api/tokens", `{"label":"x"}`, hdr); w.Code != 403 {
		t.Fatalf("no origin: %d", w.Code)
	}
}

// A fresh install has no tokens directory; the list must still encode as [] (the admin UI calls .map on it).
func TestListWithoutDirIsEmptyArray(t *testing.T) {
	tk := &Tokens{Dir: filepath.Join(t.TempDir(), "missing")}
	list, err := tk.List("github:1")
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("list = %#v, err = %v; want empty non-nil slice", list, err)
	}
}
