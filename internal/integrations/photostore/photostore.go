// Package photostore keeps odometer photos as files on local disk and signs
// short-lived URLs to view them. Photos are disposable: the cleanup job deletes
// them after PHOTO_RETENTION_DAYS, so they never fill the disk.
package photostore

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"time"
)

// ErrNotFound: there is no file for that key.
var ErrNotFound = errors.New("photo file not found")

// Store saves files under a directory. Keys are relative paths such as
// "<user id>/<photo id>.jpg", always built by the API, never by a client. Every
// operation goes through an os.Root, so no key (or symlink) can reach outside it.
type Store struct {
	root *os.Root
}

// New creates the store and its directory.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create photos dir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open photos dir: %w", err)
	}
	return &Store{root: root}, nil
}

// Save writes the file atomically: a half-written photo is never served.
func (s *Store) Save(key string, data []byte) error {
	if err := s.root.MkdirAll(path.Dir(key), 0o750); err != nil {
		return fmt.Errorf("create photo dir: %w", err)
	}
	tmpName := path.Join(path.Dir(key), ".upload-"+rand.Text())
	tmp, err := s.root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fmt.Errorf("create temp photo: %w", err)
	}
	defer s.root.Remove(tmpName) //nolint:errcheck // gone after the rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write photo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close photo: %w", err)
	}
	if err := s.root.Rename(tmpName, key); err != nil {
		return fmt.Errorf("move photo in place: %w", err)
	}
	return nil
}

// Open returns the file to serve.
func (s *Store) Open(key string) (*os.File, error) {
	file, err := s.root.Open(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return file, err
}

// Delete removes the file. A missing file is not an error.
func (s *Store) Delete(key string) error {
	if err := s.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete photo: %w", err)
	}
	return nil
}

// SweepOlderThan deletes files last modified before cutoff, and the empty
// folders left behind. It catches files whose row is already gone (a crash
// between deleting the row and the file).
func (s *Store) SweepOlderThan(cutoff time.Time) (int, error) {
	deleted := 0
	var folders []string
	err := fs.WalkDir(s.root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != "." {
				folders = append(folders, name)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished meanwhile
		}
		if info.ModTime().Before(cutoff) && s.root.Remove(name) == nil {
			deleted++
		}
		return nil
	})
	if err != nil {
		return deleted, fmt.Errorf("sweep photos: %w", err)
	}
	// Deepest first; a folder that still has files fails and stays.
	for i := len(folders) - 1; i >= 0; i-- {
		_ = s.root.Remove(folders[i])
	}
	return deleted, nil
}

// Image types accepted, by their first bytes: the client's Content-Type is not trusted.
type Image struct {
	ContentType string
	Extension   string
}

// Detect recognizes JPEG, PNG, WebP and HEIC.
func Detect(data []byte) (Image, bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return Image{"image/jpeg", "jpg"}, true
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return Image{"image/png", "png"}, true
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return Image{"image/webp", "webp"}, true
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		switch string(data[8:12]) {
		case "heic", "heix", "heim", "heis", "mif1", "msf1":
			return Image{"image/heic", "heic"}, true
		}
	}
	return Image{}, false
}

// Signer makes and checks the URLs that serve photos. A signed URL is public
// for a while, so an <img> tag can load it without the access token, and the
// share page can show photos without a session.
type Signer struct {
	key []byte
}

// NewSigner derives the signing key from the app secret, so the photo key can
// never be used as an access token key and vice versa.
func NewSigner(secret string) Signer {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("km-photo-url-v1"))
	return Signer{key: mac.Sum(nil)}
}

// Signed is a photo URL's query values.
type Signed struct {
	Expires   int64
	Signature string
}

// Sign signs access to photoID until expires.
func (s Signer) Sign(photoID string, expires time.Time) Signed {
	return Signed{Expires: expires.Unix(), Signature: s.signature(photoID, expires.Unix())}
}

// Valid checks the signature and that it has not expired.
func (s Signer) Valid(photoID, expires, signature string, now time.Time) bool {
	exp, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	want := s.signature(photoID, exp)
	return hmac.Equal([]byte(want), []byte(signature))
}

func (s Signer) signature(photoID string, expires int64) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(photoID + ":" + strconv.FormatInt(expires, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
