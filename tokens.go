package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tokens are long-lived API keys for scripts and tools (curl, Claude). Only the SHA-256 of a token is stored, so a
// leaked disk does not leak working tokens; the raw value is shown once, at creation. A token deploys, lists and
// deletes the owner's pages; it cannot create or list tokens (that needs a browser session).
type Tokens struct {
	Dir         string // one <sha256>.json per token
	MaxPerOwner int
	mu          sync.Mutex
}

// TokenInfo is what the owner sees about a token; the secret itself is never listed.
type TokenInfo struct {
	ID      string    `json:"id"` // first 12 hex chars of the hash
	Label   string    `json:"label"`
	Created time.Time `json:"created"`
}

// NewToken is the one-time response of Create.
type NewToken struct {
	TokenInfo
	Token string `json:"token"`
}

type tokenFile struct {
	TokenInfo
	Owner string `json:"owner"`
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Create makes a token for owner. The label is a note for the owner ("claude", "laptop").
func (t *Tokens) Create(owner, label string) (NewToken, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	mine, err := t.list(owner)
	if err != nil {
		return NewToken{}, err
	}
	if len(mine) >= t.MaxPerOwner {
		return NewToken{}, ErrQuota
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return NewToken{}, err
	}
	raw := "nxs_" + rand.Text() + rand.Text() // 2 x 128 bits of randomness
	h := hashToken(raw)
	info := TokenInfo{ID: h[:12], Label: label, Created: time.Now().UTC()}
	b, err := json.Marshal(tokenFile{TokenInfo: info, Owner: owner})
	if err != nil {
		return NewToken{}, err
	}
	if err := os.WriteFile(filepath.Join(t.Dir, h+".json"), b, 0o600); err != nil {
		return NewToken{}, err
	}
	return NewToken{TokenInfo: info, Token: raw}, nil
}

// Lookup returns the owner of a raw token.
func (t *Tokens) Lookup(raw string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(t.Dir, hashToken(raw)+".json"))
	if err != nil {
		return "", false
	}
	var f tokenFile
	if json.Unmarshal(b, &f) != nil || f.Owner == "" {
		return "", false
	}
	return f.Owner, true
}

func (t *Tokens) list(owner string) ([]TokenInfo, error) {
	ents, err := os.ReadDir(t.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []TokenInfo{}, nil
	} else if err != nil {
		return nil, err
	}
	out := []TokenInfo{}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(t.Dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var f tokenFile
		if json.Unmarshal(b, &f) == nil && f.Owner == owner {
			out = append(out, f.TokenInfo)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// List returns the owner's tokens, oldest first.
func (t *Tokens) List(owner string) ([]TokenInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.list(owner)
}

// Revoke deletes the owner's token with this id; another owner's token looks like a missing one.
func (t *Tokens) Revoke(owner, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(id) != 12 || strings.Trim(id, "0123456789abcdef") != "" {
		return ErrNotFound
	}
	ents, err := os.ReadDir(t.Dir)
	if err != nil {
		return ErrNotFound
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), id) {
			continue
		}
		p := filepath.Join(t.Dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var f tokenFile
		if json.Unmarshal(b, &f) == nil && f.Owner == owner {
			return os.Remove(p)
		}
	}
	return ErrNotFound
}
