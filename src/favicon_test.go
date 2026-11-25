package main

import (
	"errors"
	"strings"
	"testing"
)

func TestSetFavicon(t *testing.T) {
	const href = "data:image/svg+xml,<svg/>"
	const tag = `<link rel="icon" href="data:image/svg+xml,&lt;svg/&gt;">`
	tests := []struct{ name, in, want string }{
		{"no head", "<p>hi</p>", tag + "<p>hi</p>"},
		{"doctype, no head", "<!doctype html>\n<p>hi</p>", "<!doctype html>" + tag + "\n<p>hi</p>"},
		{"head without icon", "<html><HEAD lang=x>\n<title>t</title></head>", "<html><HEAD lang=x>" + tag + "\n<title>t</title></head>"},
		{
			"replaces every icon link, keeps apple-touch-icon",
			`<head><link rel="shortcut icon" href="a.ico"><link REL=ICON href=b.png /><link rel="apple-touch-icon" href="c.png"></head>`,
			`<head>` + tag + `<link rel="apple-touch-icon" href="c.png"></head>`,
		},
		{
			"ignores links inside script and comments",
			`<head><!-- <link rel=icon href=x> --><script>s='<link rel=icon href=y>'</script></head>`,
			`<head>` + tag + `<!-- <link rel=icon href=x> --><script>s='<link rel=icon href=y>'</script></head>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SetFavicon([]byte(tc.in), href)
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v\nwant %q", got, err, tc.want)
			}
		})
	}
}

func TestSetFaviconRejects(t *testing.T) {
	for _, href := range []string{
		"javascript:alert(1)",
		"https://example.com/x.png",
		"data:text/html,<script>",
		"data:image/png;base64," + strings.Repeat("A", 64<<10),
	} {
		if _, err := SetFavicon([]byte("<head></head>"), href); !errors.Is(err, ErrBadFavicon) {
			t.Errorf("%.40q: err = %v", href, err)
		}
	}
	got, err := SetFavicon([]byte(""), `data:image/svg+xml,<svg a="1"/>`)
	if err != nil || strings.Contains(string(got), `a="1"`) {
		t.Fatalf("quotes not escaped: %q %v", got, err)
	}
}
