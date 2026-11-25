package main

import (
	"bytes"
	"errors"
	"html"
	"regexp"
	"slices"
	"strings"

	nethtml "golang.org/x/net/html"
)

// ErrBadFavicon is returned for anything but a small data: image URL, so a favicon never points off the page.
var ErrBadFavicon = errors.New("favicon must be a data:image URL (png, svg, ico, gif, webp, jpeg) under 64 KB")

var faviconHref = regexp.MustCompile(`^data:image/(png|svg\+xml|x-icon|vnd\.microsoft\.icon|gif|webp|jpeg)[;,]`)

// SetFavicon embeds href as the page's only <link rel="icon">. Every other byte of doc stays as it was: it splices
// tags with the tokenizer's byte offsets instead of re-rendering the page. Text in scripts and comments is skipped.
func SetFavicon(doc []byte, href string) ([]byte, error) {
	if len(href) > 64<<10 || !faviconHref.MatchString(href) {
		return nil, ErrBadFavicon
	}
	type span struct{ from, to int }
	var icons []span
	afterHead, afterDoctype := -1, -1
	z := nethtml.NewTokenizer(bytes.NewReader(doc))
	for pos := 0; ; {
		tt := z.Next()
		if tt == nethtml.ErrorToken {
			break // io.EOF; the tokenizer never fails on malformed HTML
		}
		end := pos + len(z.Raw())
		switch tt {
		case nethtml.DoctypeToken:
			if afterDoctype < 0 {
				afterDoctype = end
			}
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			name, more := z.TagName()
			switch string(name) {
			case "head":
				if afterHead < 0 {
					afterHead = end
				}
			case "link":
				for more {
					var k, v []byte
					k, v, more = z.TagAttr()
					if string(k) == "rel" && slices.Contains(strings.Fields(strings.ToLower(string(v))), "icon") {
						icons = append(icons, span{pos, end})
					}
				}
			}
		}
		pos = end
	}
	if len(icons) == 0 { // insert after <head>, else after the doctype, else at the start
		at := max(afterDoctype, 0)
		if afterHead >= 0 {
			at = afterHead
		}
		icons = []span{{at, at}}
	}
	var out bytes.Buffer
	prev := 0
	for i, s := range icons { // the new tag takes the first old one's place; the others are dropped
		out.Write(doc[prev:s.from])
		if i == 0 {
			out.WriteString(`<link rel="icon" href="` + html.EscapeString(href) + `">`)
		}
		prev = s.to
	}
	out.Write(doc[prev:])
	return out.Bytes(), nil
}
