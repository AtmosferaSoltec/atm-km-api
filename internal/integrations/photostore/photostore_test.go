package photostore

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreSaveOpenDelete(t *testing.T) {
	store, err := New(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, store.Save("user/photo.jpg", []byte("abc")))
	file, err := store.Open("user/photo.jpg")
	require.NoError(t, err)
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, "abc", string(data))

	require.NoError(t, store.Delete("user/photo.jpg"))
	require.NoError(t, store.Delete("user/photo.jpg"), "deleting twice is fine")
	_, err = store.Open("user/photo.jpg")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStoreRejectsEscapingKeys(t *testing.T) {
	store, err := New(t.TempDir())
	require.NoError(t, err)
	assert.Error(t, store.Save("../outside.jpg", []byte("x")))
	assert.Error(t, store.Save("/etc/passwd", []byte("x")))
	_, err = store.Open("../../etc/passwd")
	assert.Error(t, err)
}

func TestSweepOlderThan(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	require.NoError(t, err)
	require.NoError(t, store.Save("a/old.jpg", []byte("x")))
	require.NoError(t, store.Save("b/new.jpg", []byte("x")))
	old := time.Now().Add(-100 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "a/old.jpg"), old, old))

	deleted, err := store.SweepOlderThan(time.Now().Add(-90 * 24 * time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.NoDirExists(t, filepath.Join(dir, "a"), "empty folders go too")
	assert.FileExists(t, filepath.Join(dir, "b/new.jpg"))
}

func TestDetect(t *testing.T) {
	cases := map[string][]byte{
		"image/jpeg": {0xFF, 0xD8, 0xFF, 0xE0},
		"image/png":  {0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'},
		"image/webp": []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		"image/heic": []byte("\x00\x00\x00\x18ftypheic"),
	}
	for want, data := range cases {
		image, ok := Detect(data)
		assert.True(t, ok, want)
		assert.Equal(t, want, image.ContentType)
	}
	_, ok := Detect([]byte("GIF89a"))
	assert.False(t, ok)
	_, ok = Detect(nil)
	assert.False(t, ok)
}

func TestSigner(t *testing.T) {
	signer := NewSigner("a-very-long-secret-used-only-for-the-tests")
	now := time.Unix(1_800_000_000, 0)
	signed := signer.Sign("photo-1", now.Add(time.Hour))
	expires := itoa(signed.Expires)
	assert.True(t, signer.Valid("photo-1", expires, signed.Signature, now))
	assert.False(t, signer.Valid("photo-2", expires, signed.Signature, now), "another photo")
	assert.False(t, signer.Valid("photo-1", itoa(signed.Expires+1), signed.Signature, now), "tampered expiry")
	assert.False(t, signer.Valid("photo-1", expires, signed.Signature, now.Add(2*time.Hour)), "expired")
	assert.False(t, signer.Valid("photo-1", "nope", signed.Signature, now))
	other := NewSigner("another-very-long-secret-for-the-tests!!")
	assert.False(t, other.Valid("photo-1", expires, signed.Signature, now))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
