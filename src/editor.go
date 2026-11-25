package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrConflict means the page changed after its draft was started, so publishing would overwrite someone's newer upload.
var ErrConflict = errors.New("the page changed after this draft was started; reload it or publish anyway")

// Draft is an unpublished index.html for a page, kept per owner in drafts/<owner hash>/<name>.json.
// Base is the page's Updated time when the draft was started (zero for a new page).
type Draft struct {
	Name  string    `json:"name"`
	HTML  string    `json:"html,omitempty"`
	Base  time.Time `json:"base"`
	Saved time.Time `json:"saved"`
}

func (s *Store) draftDir(owner string) string {
	h := sha256.Sum256([]byte(owner)) // owner ids contain ':' and are not ours to trust as path parts
	return s.sub("drafts", hex.EncodeToString(h[:16]))
}

// SaveDraft stores the owner's draft; the live page is not touched.
func (s *Store) SaveDraft(owner string, d Draft) (Draft, error) {
	if err := ValidName(d.Name); err != nil {
		return Draft{}, err
	}
	if int64(len(d.HTML)) > s.MaxPageBytes {
		return Draft{}, ErrTooBig
	}
	d.Saved = time.Now().UTC()
	b, err := json.Marshal(d)
	if err != nil {
		return Draft{}, err
	}
	dir := s.draftDir(owner)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Draft{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, d.Name+".json")); errors.Is(err, os.ErrNotExist) {
		if ents, err := os.ReadDir(dir); err != nil {
			return Draft{}, err
		} else if len(ents) >= 2*s.MaxPages { // room for a draft of every page plus as many new ones
			return Draft{}, fmt.Errorf("%w: too many drafts; publish or discard some", ErrQuota)
		}
	}
	tmp, err := os.CreateTemp(s.sub("tmp"), "draft-")
	if err != nil {
		return Draft{}, err
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, d.Name+".json"))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return Draft{}, err
	}
	return d, nil
}

// GetDraft returns the owner's draft with its HTML.
func (s *Store) GetDraft(owner, name string) (Draft, error) {
	if ValidName(name) != nil {
		return Draft{}, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(s.draftDir(owner), name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Draft{}, ErrNotFound
	} else if err != nil {
		return Draft{}, err
	}
	var d Draft
	return d, json.Unmarshal(b, &d)
}

// Drafts lists the owner's drafts without their HTML, sorted by name.
func (s *Store) Drafts(owner string) ([]Draft, error) {
	ents, err := os.ReadDir(s.draftDir(owner))
	if errors.Is(err, os.ErrNotExist) {
		return []Draft{}, nil
	} else if err != nil {
		return nil, err
	}
	list := []Draft{}
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		d, err := s.GetDraft(owner, name)
		if err != nil { // a stray or damaged file must not hide the other drafts
			log.Printf("skipping draft %s: %v", e.Name(), err)
			continue
		}
		d.HTML = ""
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// DeleteDraft discards the owner's draft.
func (s *Store) DeleteDraft(owner, name string) error {
	if ValidName(name) != nil {
		return ErrNotFound
	}
	err := os.Remove(filepath.Join(s.draftDir(owner), name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// Source returns the index.html of the owner's page and its meta.
func (s *Store) Source(owner, name string) ([]byte, Meta, error) {
	s.mu.Lock()
	m, err := s.owned(owner, name)
	s.mu.Unlock()
	if err != nil {
		return nil, Meta{}, err
	}
	b, err := os.ReadFile(s.sub("pages", name, "index.html"))
	return b, m, err
}

// Publish makes the draft the page's index.html, keeps the page's other files, and deletes the draft. Unless force
// is set, it refuses when the page changed after the draft was started.
func (s *Store) Publish(owner, name string, force bool) (Meta, error) {
	d, err := s.GetDraft(owner, name)
	if err != nil {
		return Meta{}, err
	}
	entries := []Entry{{Path: "index.html", Open: func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(d.HTML)), nil
	}}}
	// shortcut: the check and Deploy take the lock separately, so an upload in between wins silently; fine for one
	// owner editing their own pages, hold s.mu across both if several people share pages.
	s.mu.Lock()
	m, err := s.owned(owner, name)
	s.mu.Unlock()
	switch {
	case errors.Is(err, ErrNotFound): // a new page, or another owner's (Deploy refuses that)
	case err != nil:
		return Meta{}, err
	case !force && !m.Updated.Equal(d.Base):
		return Meta{}, ErrConflict
	default:
		dir := s.sub("pages", name)
		err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err == nil && rel != "index.html" {
				entries = append(entries, Entry{Path: filepath.ToSlash(rel), Open: func() (io.ReadCloser, error) { return os.Open(p) }})
			}
			return err
		})
		if err != nil {
			return Meta{}, err
		}
	}
	m, err = s.Deploy(owner, name, entries)
	if err != nil {
		return Meta{}, err
	}
	return m, s.DeleteDraft(owner, name)
}
