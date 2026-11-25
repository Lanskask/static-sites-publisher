package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Errors returned to the user; the HTTP layer maps them to status codes.
var (
	ErrBadName  = errors.New("page name must be 1-63 of a-z, 0-9 and '-', not starting or ending with '-', not reserved")
	ErrBadPath  = errors.New("upload has an unsafe path (absolute, '..', backslash or a link)")
	ErrNoIndex  = errors.New("upload needs an index.html at its root")
	ErrTooBig   = errors.New("upload is over the size limit")
	ErrTooMany  = errors.New("upload has too many files")
	ErrQuota    = errors.New("page limit reached")
	ErrTaken    = errors.New("page name belongs to another user")
	ErrNotFound = errors.New("page not found")
	ErrBadTag   = errors.New("tags: at most 10, each 1-30 of a-z, 0-9 and '-'")
	ErrBadZip   = errors.New("not a valid zip file")
)

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

var reserved = map[string]bool{"admin": true, "www": true, "api": true, "mail": true, "static": true}

// ValidName reports whether name can be a DNS label for a page.
func ValidName(name string) error {
	if !nameRe.MatchString(name) || reserved[name] {
		return ErrBadName
	}
	return nil
}

// Entry is one uploaded file: its path inside the page and a way to read it.
type Entry struct {
	Path string
	Open func() (io.ReadCloser, error)
}

// Meta describes a deployed page; it lives in meta/<name>.json, outside the served tree.
type Meta struct {
	Name    string    `json:"name"`
	Owner   string    `json:"owner"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Bytes   int64     `json:"bytes"`
	Tags    []string  `json:"tags,omitempty"`
}

// Store keeps pages on disk: Dir/pages/<name>/ (served), Dir/meta/<name>.json, Dir/tmp/ (uploads in progress).
type Store struct {
	Dir          string
	MaxPages     int                    // per owner
	MaxPageBytes int64                  // unzipped, per page
	MaxUserBytes int64                  // all pages of one owner
	MaxFiles     int                    // per page
	Scan         func(abs string) error // optional extra scanner (clamd), run on every file after the built-in rules
	mu           sync.Mutex
}

// Init creates the directories and drops uploads left over from a crash.
func (s *Store) Init() error {
	if err := os.RemoveAll(s.sub("tmp")); err != nil {
		return err
	}
	for _, d := range []string{"pages", "meta", "tmp"} {
		if err := os.MkdirAll(s.sub(d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Exists reports whether a page is published; name must already be valid.
func (s *Store) Exists(name string) bool {
	_, err := os.Stat(s.sub("meta", name+".json"))
	return err == nil
}

func (s *Store) sub(parts ...string) string {
	return filepath.Join(append([]string{s.Dir}, parts...)...)
}

// Normalize checks upload paths and maps them to their place in the page, keeping the order:
// a lone .html file becomes index.html, and one shared top folder (a dropped folder or zip) is removed.
func Normalize(paths []string) ([]string, error) {
	out := make([]string, len(paths))
	for i, p := range paths {
		c := path.Clean(p)
		if strings.Contains(p, `\`) || path.IsAbs(p) || c == "." || c == ".." || strings.HasPrefix(c, "../") {
			return nil, ErrBadPath
		}
		out[i] = c
	}
	if len(out) == 1 && (strings.HasSuffix(out[0], ".html") || strings.HasSuffix(out[0], ".htm")) {
		return []string{"index.html"}, nil
	}
	if top, _, ok := strings.Cut(out[0], "/"); ok {
		shared := true
		for _, p := range out {
			shared = shared && strings.HasPrefix(p, top+"/")
		}
		if shared {
			for i := range out {
				out[i] = strings.TrimPrefix(out[i], top+"/")
			}
		}
	}
	if !slices.Contains(out, "index.html") {
		return nil, ErrNoIndex
	}
	return out, nil
}

// junk is what macOS adds to folders and zips.
func junk(p string) bool {
	return strings.HasPrefix(p, "__MACOSX/") || path.Base(p) == ".DS_Store"
}

// ZipEntries lists the regular files of a zip; links and other special files are refused.
func ZipEntries(r io.ReaderAt, size int64) ([]Entry, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadZip, err)
	}
	var es []Entry
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, ErrBadPath
		}
		es = append(es, Entry{Path: f.Name, Open: f.Open})
	}
	return es, nil
}

// Deploy publishes the upload as page name, replacing the owner's earlier version in one rename.
func (s *Store) Deploy(owner, name string, in []Entry) (Meta, error) {
	if err := ValidName(name); err != nil {
		return Meta{}, err
	}
	var kept []Entry
	for _, e := range in {
		if !junk(e.Path) {
			kept = append(kept, e)
		}
	}
	if len(kept) > s.MaxFiles {
		return Meta{}, ErrTooMany
	}
	paths := make([]string, len(kept))
	for i, e := range kept {
		paths[i] = e.Path
	}
	norm, err := Normalize(paths)
	if err != nil {
		return Meta{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.all()
	if err != nil {
		return Meta{}, err
	}
	var old *Meta
	var used int64
	count := 0
	for i, m := range all {
		switch {
		case m.Name == name && m.Owner != owner:
			return Meta{}, ErrTaken
		case m.Name == name:
			old = &all[i]
		case m.Owner == owner:
			count++
			used += m.Bytes
		}
	}
	if old == nil && count >= s.MaxPages {
		return Meta{}, ErrQuota
	}

	tmp, err := os.MkdirTemp(s.sub("tmp"), name+"-")
	if err != nil {
		return Meta{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.Chmod(tmp, 0o755); err != nil { // MkdirTemp makes 0700; nginx runs as another user
		return Meta{}, err
	}
	var total int64
	for i, e := range kept {
		n, err := copyLimited(filepath.Join(tmp, filepath.FromSlash(norm[i])), e, s.MaxPageBytes-total)
		if err != nil {
			return Meta{}, err
		}
		total += n
	}
	if used+total > s.MaxUserBytes {
		return Meta{}, ErrTooBig
	}
	if err := scanTree(tmp, s.Scan); err != nil {
		return Meta{}, err
	}

	final := s.sub("pages", name)
	trash := tmp + ".old"
	if old != nil {
		if err := os.Rename(final, trash); err != nil {
			return Meta{}, err
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		return Meta{}, err
	}
	_ = os.RemoveAll(trash) // the new version is live; a leftover is removed by the next Init

	now := time.Now().UTC()
	m := Meta{Name: name, Owner: owner, Created: now, Updated: now, Bytes: total}
	if old != nil {
		m.Created, m.Tags = old.Created, old.Tags
	}
	return m, s.writeMeta(m)
}

func copyLimited(dst string, e Entry, remaining int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	r, err := e.Open()
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", e.Path, err)
	}
	defer func() { _ = r.Close() }()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, remaining+1)) // reading stops early, so a zip bomb never expands
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("write %s: %w", e.Path, err)
	}
	if n > remaining {
		return n, ErrTooBig
	}
	return n, nil
}

func (s *Store) writeMeta(m Meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := s.sub("tmp", m.Name+".json")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.sub("meta", m.Name+".json"))
}

func (s *Store) all() ([]Meta, error) {
	ents, err := os.ReadDir(s.sub("meta"))
	if err != nil {
		return nil, err
	}
	var ms []Meta
	for _, e := range ents {
		b, err := os.ReadFile(s.sub("meta", e.Name()))
		if err != nil {
			return nil, err
		}
		var m Meta
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		ms = append(ms, m)
	}
	return ms, nil
}

// List returns the owner's pages sorted by name.
func (s *Store) List(owner string) ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.all()
	if err != nil {
		return nil, err
	}
	mine := []Meta{}
	for _, m := range all {
		if m.Owner == owner {
			mine = append(mine, m)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Name < mine[j].Name })
	return mine, nil
}

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)

// SetTags replaces the tags of the owner's page; they are lowercased, deduplicated and sorted.
func (s *Store) SetTags(owner, name string, tags []string) (Meta, error) {
	set := map[string]bool{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if !tagRe.MatchString(t) {
			return Meta{}, ErrBadTag
		}
		set[t] = true
	}
	if len(set) > 10 {
		return Meta{}, ErrBadTag
	}
	clean := slices.Sorted(maps.Keys(set))
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.owned(owner, name)
	if err != nil {
		return Meta{}, err
	}
	m.Tags = clean
	return m, s.writeMeta(m)
}

// owned reads the meta of the owner's page; another owner's page looks like a missing one. Callers hold s.mu.
func (s *Store) owned(owner, name string) (Meta, error) {
	if ValidName(name) != nil {
		return Meta{}, ErrNotFound
	}
	b, err := os.ReadFile(s.sub("meta", name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Meta{}, ErrNotFound
	} else if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, err
	}
	if m.Owner != owner {
		return Meta{}, ErrNotFound
	}
	return m, nil
}

// Delete removes the owner's page; another owner's page looks like a missing one.
func (s *Store) Delete(owner, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.owned(owner, name); err != nil {
		return err
	}
	// files first: if this stops halfway, the name still has an owner
	if err := os.RemoveAll(s.sub("pages", name)); err != nil {
		return err
	}
	return os.Remove(s.sub("meta", name+".json"))
}
