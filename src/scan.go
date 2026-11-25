package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrRejected means the upload looks like malware or phishing; the message says why.
var ErrRejected = errors.New("upload rejected")

// A static site never needs these: they are programs or server-side scripts, not web content.
var blockedExt = map[string]bool{
	".exe": true, ".dll": true, ".msi": true, ".scr": true, ".com": true, ".bat": true, ".cmd": true, ".ps1": true,
	".sh": true, ".jar": true, ".apk": true, ".dmg": true, ".pkg": true, ".app": true, ".vbs": true, ".lnk": true,
	".php": true, ".phtml": true, ".asp": true, ".aspx": true, ".jsp": true, ".cgi": true, ".pl": true, ".py": true,
}

var magics = map[string][]byte{
	"Windows/DOS executable":   []byte("MZ"),
	"Linux executable":         []byte("\x7fELF"),
	"shell or script file":     []byte("#!"),
	"macOS executable":         {0xcf, 0xfa, 0xed, 0xfe},
	"Java class or fat Mach-O": {0xca, 0xfe, 0xba, 0xbe},
}

// A password field on a throwaway subdomain is how credential-phishing pages look; this service has no use for one.
var passwordInput = regexp.MustCompile(`(?i)<input[^>]*\btype\s*=\s*["']?password\b`)

// ScanFile checks one stored file with the built-in rules. rel is its path inside the page.
func ScanFile(abs, rel string) error {
	if ext := strings.ToLower(path.Ext(rel)); blockedExt[ext] {
		return fmt.Errorf("%w: %s files are not allowed (%s)", ErrRejected, ext, rel)
	}
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 4) // the longest magic number
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	for kind, m := range magics {
		if bytes.HasPrefix(head[:n], m) {
			return fmt.Errorf("%w: %s looks like a %s", ErrRejected, rel, kind)
		}
	}
	if ext := strings.ToLower(path.Ext(rel)); ext != ".html" && ext != ".htm" && ext != ".svg" {
		return nil
	}
	rest, err := io.ReadAll(f) // the whole file (the page size limit caps it), so padding cannot hide the field
	if err != nil {
		return err
	}
	if passwordInput.Match(append(head[:n], rest...)) {
		return fmt.Errorf("%w: %s has a password field (phishing risk)", ErrRejected, rel)
	}
	return nil
}

// scanTree runs the built-in rules, then the optional extra scanner, over every file under root.
func scanTree(root string, extra func(abs string) error) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if err := ScanFile(p, filepath.ToSlash(rel)); err != nil {
			return err
		}
		if extra != nil {
			return extra(p)
		}
		return nil
	})
}

// ClamdScanner streams each file to a clamd daemon (INSTREAM). A virus verdict is ErrRejected; an unreachable
// daemon is a plain error, so the upload fails closed instead of skipping the scan.
func ClamdScanner(addr string) func(abs string) error {
	return func(abs string) error {
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return fmt.Errorf("virus scanner unavailable: %w", err)
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(60 * time.Second))
		if _, err := c.Write([]byte("zINSTREAM\x00")); err != nil {
			return fmt.Errorf("virus scanner: %w", err)
		}
		buf := make([]byte, 64<<10)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				var l [4]byte
				binary.BigEndian.PutUint32(l[:], uint32(n))
				if _, err := c.Write(append(l[:], buf[:n]...)); err != nil {
					return fmt.Errorf("virus scanner: %w", err)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return rerr
			}
		}
		if _, err := c.Write([]byte{0, 0, 0, 0}); err != nil {
			return fmt.Errorf("virus scanner: %w", err)
		}
		reply, err := io.ReadAll(io.LimitReader(c, 4096))
		if err != nil {
			return fmt.Errorf("virus scanner: %w", err)
		}
		msg := strings.TrimSpace(strings.TrimRight(string(reply), "\x00"))
		switch {
		case strings.HasSuffix(msg, "OK"):
			return nil
		case strings.HasSuffix(msg, "FOUND"):
			sig := strings.TrimSuffix(strings.TrimPrefix(msg, "stream: "), " FOUND")
			return fmt.Errorf("%w: virus found (%s)", ErrRejected, sig)
		default:
			return fmt.Errorf("virus scanner: %s", msg)
		}
	}
}
