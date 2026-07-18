package imagestore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "images"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPutReadRoundTrip(t *testing.T) {
	s := newStore(t)
	data := []byte("fake-png-bytes")
	hash, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if want := hex.EncodeToString(sum[:]); hash != want {
		t.Fatalf("hash = %q, want %q", hash, want)
	}
	got, err := s.Read(hash)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("Read = %q, want %q", got, data)
	}
}

func TestPutIsIdempotent(t *testing.T) {
	s := newStore(t)
	data := []byte("same-bytes")
	h1, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hashes differ: %q vs %q", h1, h2)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1", len(entries))
	}
}

func TestReadMissingHashIsNotExist(t *testing.T) {
	s := newStore(t)
	_, err := s.Read("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestReadRejectsInvalidHash(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"", "abc", "../../etc/passwd", "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"} {
		if _, err := s.Read(bad); err == nil {
			t.Fatalf("Read(%q) succeeded, want error", bad)
		}
	}
}

func TestHandlerServesStoredImage(t *testing.T) {
	s := newStore(t)
	// Real PNG magic bytes followed by minimal payload
	pngData := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, []byte("png-payload")...)
	hash, _ := s.Put(pngData)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/appimages/" + hash + ".png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(pngData) {
		t.Fatalf("body mismatch")
	}
}

func TestHandlerServesSniffedContentType(t *testing.T) {
	s := newStore(t)
	// Minimal JPEG magic — enough for http.DetectContentType.
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("fakejpegbody")...)
	hash, _ := s.Put(jpeg)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/appimages/" + hash + ".png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg (sniffed)", ct)
	}
}

func TestHandler404s(t *testing.T) {
	s := newStore(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	for _, path := range []string{
		"/appimages/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.png", // unknown hash
		"/appimages/notahash.png", // malformed hash
		"/appimages/../app.db",    // traversal shape
		"/somewhere/else.png",     // wrong prefix
		"/appimages/",             // empty
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
	}
}
