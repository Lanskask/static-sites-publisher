package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newAuth() *Auth {
	return &Auth{
		Key:    []byte("0123456789abcdef0123456789abcdef"),
		Allow:  map[string]bool{"github:1": true},
		Origin: "https://admin.test",
	}
}

func TestSessionCookie(t *testing.T) {
	a := newAuth()
	v := a.Sign("github:1", time.Now().Add(time.Hour))
	if id, ok := a.Verify(v); !ok || id != "github:1" {
		t.Fatalf("Verify(valid) = %q, %v", id, ok)
	}
	for name, bad := range map[string]string{
		"tampered id":  strings.Replace(v, v[:4], "AAAA", 1),
		"tampered mac": v[:len(v)-2] + "xx",
		"expired":      a.Sign("github:1", time.Now().Add(-time.Second)),
		"other key":    (&Auth{Key: []byte("another key of 32 bytes........")}).Sign("github:1", time.Now().Add(time.Hour)),
		"garbage":      "nope",
	} {
		if _, ok := a.Verify(bad); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRequire(t *testing.T) {
	a := newAuth()
	h := a.Require(func(w http.ResponseWriter, _ *http.Request, id string) { _, _ = w.Write([]byte(id)) })
	cookie := func(id string) *http.Cookie {
		return &http.Cookie{Name: sessionCookie, Value: a.Sign(id, time.Now().Add(time.Hour))}
	}
	tests := []struct {
		name   string
		method string
		cookie *http.Cookie
		origin string
		want   int
	}{
		{"no cookie", "GET", nil, "", 401},
		{"not on allowlist", "GET", cookie("github:2"), "", 403},
		{"allowed GET", "GET", cookie("github:1"), "", 200},
		{"POST without Origin", "POST", cookie("github:1"), "", 403},
		{"POST foreign Origin", "POST", cookie("github:1"), "https://evil.test", 403},
		{"POST own Origin", "POST", cookie("github:1"), "https://admin.test", 200},
		{"DELETE foreign Origin", "DELETE", cookie("github:1"), "https://x.pages.test", 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/pages", nil)
			if tc.cookie != nil {
				r.AddCookie(tc.cookie)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestCallbackChecksState(t *testing.T) {
	a := newAuth()
	a.Providers = map[string]*Provider{"github": {}}
	r := httptest.NewRequest("GET", "/auth/github/callback?state=abc&code=x", nil)
	r.SetPathValue("provider", "github")
	r.AddCookie(&http.Cookie{Name: stateCookie, Value: "different"})
	w := httptest.NewRecorder()
	a.Callback(w, r)
	if w.Code != 400 {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestParseAllowlist(t *testing.T) {
	got := ParseAllowlist(" github:1, google:abc ,,")
	if len(got) != 2 || !got["github:1"] || !got["google:abc"] {
		t.Fatalf("got %v", got)
	}
}
