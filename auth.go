package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	sessionCookie = "__Host-session" // __Host-: Secure, Path=/, no Domain, so page subdomains can never set it
	stateCookie   = "__Host-state"
	sessionTTL    = 7 * 24 * time.Hour
)

// Provider is one OAuth login (GitHub or Google) and where to read the user's stable id.
type Provider struct {
	Config  oauth2.Config
	UserURL string
}

// Auth signs sessions, runs the OAuth flow and guards the API.
type Auth struct {
	Key       []byte          // HMAC key for session cookies
	Allow     map[string]bool // identities ("github:<id>", "google:<sub>") let in; v1 is owner only
	Origin    string          // the admin's own origin, e.g. https://nex-admin.duckdns.org
	Providers map[string]*Provider
	Dev       string // fixed identity for local runs; main sets it only for a localhost Origin
}

// ParseAllowlist reads "github:1,google:abc" into a set.
func ParseAllowlist(s string) map[string]bool {
	m := map[string]bool{}
	for id := range strings.SplitSeq(s, ",") {
		if id = strings.TrimSpace(id); id != "" {
			m[id] = true
		}
	}
	return m
}

func (a *Auth) mac(payload string) []byte {
	h := hmac.New(sha256.New, a.Key)
	h.Write([]byte(payload))
	return h.Sum(nil)
}

// Sign returns a cookie value carrying id until exp.
func (a *Auth) Sign(id string, exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(id + "|" + strconv.FormatInt(exp.Unix(), 10)))
	return payload + "." + base64.RawURLEncoding.EncodeToString(a.mac(payload))
}

// Verify returns the id of an untampered, unexpired cookie value.
func (a *Auth) Verify(v string) (string, bool) {
	payload, sig, ok := strings.Cut(v, ".")
	if !ok {
		return "", false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, a.mac(payload)) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	id, expS, ok := strings.Cut(string(raw), "|")
	exp, err := strconv.ParseInt(expS, 10, 64)
	if !ok || err != nil || time.Now().Unix() >= exp {
		return "", false
	}
	return id, true
}

// Identity returns the signed-in identity, or "".
func (a *Auth) Identity(r *http.Request) string {
	if a.Dev != "" {
		return a.Dev
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	id, _ := a.Verify(c.Value)
	return id
}

// Require lets through only allowlisted users; state-changing requests must also come from the admin's own origin.
func (a *Auth) Require(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := a.Identity(r)
		switch {
		case id == "":
			http.Error(w, "log in first", http.StatusUnauthorized)
		case !a.Allow[id]:
			http.Error(w, "not on the allowlist: "+id, http.StatusForbidden)
		case r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != a.Origin:
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
		default:
			next(w, r, id)
		}
	})
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// Start sends the browser to the provider's login page.
func (a *Auth) Start(w http.ResponseWriter, r *http.Request) {
	p := a.Providers[r.PathValue("provider")]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	state := rand.Text()
	setCookie(w, stateCookie, state, 600)
	http.Redirect(w, r, p.Config.AuthCodeURL(state), http.StatusFound)
}

// Callback finishes the OAuth flow and sets the session cookie.
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	p := a.Providers[r.PathValue("provider")]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	c, err := r.Cookie(stateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "login expired or forged, try again", http.StatusBadRequest)
		return
	}
	setCookie(w, stateCookie, "", -1)
	id, err := p.identity(r, r.PathValue("provider"))
	if err != nil {
		log.Printf("login: %v", err)
		http.Error(w, "login failed", http.StatusBadGateway)
		return
	}
	if !a.Allow[id] {
		// shows the id so the owner can copy it into ALLOWLIST
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, "<p>Signed in as <code>%s</code>, which is not on the allowlist yet.</p>", html.EscapeString(id))
		return
	}
	setCookie(w, sessionCookie, a.Sign(id, time.Now().Add(sessionTTL)), int(sessionTTL.Seconds()))
	http.Redirect(w, r, "/", http.StatusFound)
}

// Logout clears the session cookie.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != a.Origin {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	setCookie(w, sessionCookie, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (p *Provider) identity(r *http.Request, name string) (string, error) {
	tok, err := p.Config.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		return "", fmt.Errorf("exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, p.UserURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.Config.Client(r.Context(), tok).Do(req)
	if err != nil {
		return "", fmt.Errorf("user info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("user info: %s", resp.Status)
	}
	var u struct {
		ID  json.Number `json:"id"`  // GitHub
		Sub string      `json:"sub"` // Google
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", fmt.Errorf("user info: %w", err)
	}
	if u.Sub == "" && u.ID == "" {
		return "", fmt.Errorf("user info has no id")
	}
	return name + ":" + string(u.ID) + u.Sub, nil
}
