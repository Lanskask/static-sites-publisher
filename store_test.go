package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Dir: t.TempDir(), MaxPages: 2, MaxPageBytes: 100, MaxUserBytes: 150, MaxFiles: 3}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

func files(kv ...string) []Entry {
	var es []Entry
	for i := 0; i < len(kv); i += 2 {
		body := kv[i+1]
		es = append(es, Entry{Path: kv[i], Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		}})
	}
	return es
}

func TestValidName(t *testing.T) {
	for name, ok := range map[string]bool{
		"blog": true, "my-site-2": true, "a": true,
		"": false, "-a": false, "a-": false, "A": false, "a_b": false, "a.b": false,
		"admin": false, "www": false, strings.Repeat("a", 64): false,
	} {
		if got := ValidName(name) == nil; got != ok {
			t.Errorf("ValidName(%q) ok=%v, want %v", name, got, ok)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
		err  bool
	}{
		{"single html becomes index", []string{"page.html"}, []string{"index.html"}, false},
		{"top folder unwrapped", []string{"site/index.html", "site/a.css"}, []string{"index.html", "a.css"}, false},
		{"flat kept", []string{"index.html", "js/app.js"}, []string{"index.html", "js/app.js"}, false},
		{"dotdot", []string{"index.html", "../evil"}, nil, true},
		{"nested dotdot", []string{"index.html", "a/../../evil"}, nil, true},
		{"absolute", []string{"index.html", "/etc/passwd"}, nil, true},
		{"backslash", []string{"index.html", `a\..\b`}, nil, true},
		{"no index", []string{"a.css"}, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.in)
			if (err != nil) != tc.err {
				t.Fatalf("err=%v, want err=%v", err, tc.err)
			}
			if !tc.err && strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeployAndReplace(t *testing.T) {
	s := newStore(t)
	if _, err := s.Deploy("u1", "blog", files("site/index.html", "v1", "site/a.css", "x")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deploy("u1", "blog", files("index.html", "v2")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(s.Dir, "pages", "blog", "index.html"))
	if err != nil || string(got) != "v2" {
		t.Fatalf("index.html = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "pages", "blog", "a.css")); !os.IsNotExist(err) {
		t.Fatal("replace must drop old files")
	}
	pages, err := s.List("u1")
	if err != nil || len(pages) != 1 || pages[0].Bytes != 2 {
		t.Fatalf("List = %+v, %v", pages, err)
	}
	leftovers, _ := os.ReadDir(filepath.Join(s.Dir, "tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("tmp not cleaned: %v", leftovers)
	}
}

func TestDeployLimits(t *testing.T) {
	big := strings.Repeat("x", 101)
	tests := []struct {
		name  string
		setup func(s *Store)
		owner string
		in    []Entry
		want  error
	}{
		{"page too big", nil, "u1", files("index.html", big), ErrTooBig},
		{"too many files", nil, "u1", files("index.html", "", "a", "", "b", "", "c", ""), ErrTooMany},
		{"quota pages", func(s *Store) {
			must(t, s, "u1", "p1")
			must(t, s, "u1", "p2")
		}, "u1", files("index.html", "x"), ErrQuota},
		{"user bytes", func(s *Store) {
			_, err := s.Deploy("u1", "p1", files("index.html", strings.Repeat("x", 100)))
			if err != nil {
				t.Fatal(err)
			}
		}, "u1", files("index.html", strings.Repeat("x", 60)), ErrTooBig},
		{"name taken by other owner", func(s *Store) { must(t, s, "u2", "new") }, "u1", files("index.html", "x"), ErrTaken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			if tc.setup != nil {
				tc.setup(s)
			}
			_, err := s.Deploy(tc.owner, "new", tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(s.Dir, "pages", "new", "index.html")); tc.want != ErrTaken && !os.IsNotExist(err) {
				t.Fatal("refused upload must not be published")
			}
		})
	}
}

func TestReplaceDoesNotCountItself(t *testing.T) {
	s := newStore(t)
	must(t, s, "u1", "p1")
	must(t, s, "u1", "p2")
	must(t, s, "u1", "p2") // at the page quota, but replacing is allowed
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	must(t, s, "u1", "blog")
	if err := s.Delete("u2", "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner delete: %v", err)
	}
	if err := s.Delete("u1", "blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "pages", "blog")); !os.IsNotExist(err) {
		t.Fatal("page dir still there")
	}
	if pages, _ := s.List("u1"); len(pages) != 0 {
		t.Fatalf("List after delete = %v", pages)
	}
}

func TestZipEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("site/index.html")
	_, _ = w.Write([]byte("hi"))
	_, _ = zw.Create("site/sub/") // directory entry, skipped
	link := &zip.FileHeader{Name: "site/link"}
	link.SetMode(os.ModeSymlink | 0o777)
	w, _ = zw.CreateHeader(link)
	_, _ = w.Write([]byte("/etc/passwd"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(buf.Bytes())
	if _, err := ZipEntries(r, r.Size()); !errors.Is(err, ErrBadPath) {
		t.Fatalf("symlink in zip: err = %v", err)
	}
}

func must(t *testing.T, s *Store, owner, name string) {
	t.Helper()
	if _, err := s.Deploy(owner, name, files("index.html", "x")); err != nil {
		t.Fatal(err)
	}
}

func TestSetTags(t *testing.T) {
	s := newStore(t)
	if _, err := s.Deploy("me", "blog", files("index.html", "x")); err != nil {
		t.Fatal(err)
	}
	m, err := s.SetTags("me", "blog", []string{" Go ", "learning", "go", ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Tags, ",") != "go,learning" {
		t.Fatalf("tags = %v, want trimmed, lowercased, deduped, sorted", m.Tags)
	}
	// a redeploy keeps the tags
	if _, err := s.Deploy("me", "blog", files("index.html", "y")); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.List("me"); strings.Join(ms[0].Tags, ",") != "go,learning" {
		t.Fatalf("tags after redeploy = %v", ms[0].Tags)
	}
	if _, err := s.SetTags("other", "blog", []string{"x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner: err = %v, want ErrNotFound", err)
	}
	for _, bad := range [][]string{{"a b"}, {"<script>"}, {strings.Repeat("a", 31)}, strings.Split("a,b,c,d,e,f,g,h,i,j,k", ",")} {
		if _, err := s.SetTags("me", "blog", bad); !errors.Is(err, ErrBadTag) {
			t.Errorf("SetTags(%q) err = %v, want ErrBadTag", bad, err)
		}
	}
}
