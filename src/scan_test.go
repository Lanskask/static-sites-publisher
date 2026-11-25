package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

func TestDeployRejectsMalicious(t *testing.T) {
	tests := []struct {
		name string
		in   []Entry
	}{
		{"exe by extension", files("index.html", "<p>hi", "setup.exe", "x")},
		{"php script", files("index.html", "<p>hi", "shell.php", "<?php")},
		{"ELF disguised as png", files("index.html", "<p>hi", "logo.png", "\x7fELF\x02\x01")},
		{"PE disguised as css", files("index.html", "<p>hi", "a.css", "MZ\x90\x00")},
		{"shebang script", files("index.html", "<p>hi", "run.txt", "#!/bin/sh\nrm -rf /")},
		{"password form", files("index.html", `<form><INPUT type="Password" name=p></form>`)},
		{"password form single quotes", files("index.html", `<input name=p type='password'>`)},
		{"password form unquoted", files("index.html", `<input type=password>`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			s.MaxPageBytes, s.MaxUserBytes = 1000, 1000
			_, err := s.Deploy("u1", "bad", tc.in)
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("err=%v, want ErrRejected", err)
			}
			if l, _ := s.List("u1"); len(l) != 0 {
				t.Fatalf("rejected upload was stored: %v", l)
			}
		})
	}
}

func TestDeployAllowsNormalSite(t *testing.T) {
	s := newStore(t)
	s.MaxPageBytes, s.MaxUserBytes = 1000, 1000
	in := files("index.html", "<h1>hello</h1><script src=a.js></script>", "a.js", "alert(1)", "logo.png", "\x89PNG\r\n")
	if _, err := s.Deploy("u1", "ok", in); err != nil {
		t.Fatal(err)
	}
}

// fakeClamd answers the INSTREAM protocol: zINSTREAM\0, then [len][data]... [0000], reply "stream: <verdict>\0".
func fakeClamd(t *testing.T, verdict func(data string) string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				r := bufio.NewReader(c)
				if cmd, _ := r.ReadString(0); cmd != "zINSTREAM\x00" {
					return
				}
				var sb strings.Builder
				for {
					var n [4]byte
					if _, err := io.ReadFull(r, n[:]); err != nil {
						return
					}
					size := int(n[0])<<24 | int(n[1])<<16 | int(n[2])<<8 | int(n[3])
					if size == 0 {
						break
					}
					buf := make([]byte, size)
					if _, err := io.ReadFull(r, buf); err != nil {
						return
					}
					sb.Write(buf)
				}
				_, _ = c.Write([]byte("stream: " + verdict(sb.String()) + "\x00"))
			}()
		}
	}()
	return l.Addr().String()
}

func TestClamd(t *testing.T) {
	addr := fakeClamd(t, func(d string) string {
		if strings.Contains(d, "EICAR") {
			return "Eicar-Test-Signature FOUND"
		}
		return "OK"
	})
	s := newStore(t)
	s.MaxPageBytes, s.MaxUserBytes = 1000, 1000
	s.Scan = ClamdScanner(addr)

	if _, err := s.Deploy("u1", "clean", files("index.html", "<p>fine")); err != nil {
		t.Fatalf("clean upload: %v", err)
	}
	_, err := s.Deploy("u1", "dirty", files("index.html", "<p>EICAR"))
	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "Eicar-Test-Signature") {
		t.Fatalf("err=%v, want ErrRejected naming the signature", err)
	}
}

func TestClamdDownRejectsUpload(t *testing.T) { // fail closed: no scan, no publish
	s := newStore(t)
	s.Scan = ClamdScanner("127.0.0.1:1")
	if _, err := s.Deploy("u1", "p", files("index.html", "<p>x")); err == nil || errors.Is(err, ErrRejected) {
		t.Fatalf("err=%v, want a scanner error that is not ErrRejected", err)
	}
}

func TestPasswordFieldAfterPaddingIsFound(t *testing.T) {
	s := newStore(t)
	s.MaxPageBytes, s.MaxUserBytes = 4<<20, 4<<20
	page := "<!--" + strings.Repeat("x", 2<<20) + "--><input type=password>"
	if _, err := s.Deploy("u1", "pad", files("index.html", page)); !errors.Is(err, ErrRejected) {
		t.Fatalf("err=%v, want ErrRejected", err)
	}
}
