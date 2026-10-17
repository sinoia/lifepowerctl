package hostimage

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func TestReplaceOpenAndRemove(t *testing.T) {
	directory := t.TempDir()
	store, err := New(directory, limits.Default().HostImage)
	if err != nil {
		t.Fatal(err)
	}
	encoded := testPNG(t, 3, 2)
	info, err := store.Replace(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if info.ContentType != "image/png" || info.Width != 3 || info.Height != 2 || info.Size != int64(len(encoded)) {
		t.Fatalf("stored info = %+v", info)
	}
	stat, err := os.Stat(filepath.Join(directory, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("stored mode = %04o", stat.Mode().Perm())
	}
	file, opened, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if opened.ContentType != info.ContentType || opened.Width != info.Width || opened.Height != info.Height {
		t.Fatalf("opened info = %+v, want %+v", opened, info)
	}
	if err := store.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Open(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open removed image error = %v", err)
	}
	if err := store.Remove(); err != nil {
		t.Fatalf("remove missing image: %v", err)
	}
}

func TestReplaceRejectsInvalidOversizedAndExcessivePixelImages(t *testing.T) {
	directory := t.TempDir()
	imageLimits := limits.HostImage{MaxBytes: 128, MaxPixels: 3}
	store, err := New(directory, imageLimits)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{
		"invalid":   []byte("not an image"),
		"oversized": bytes.Repeat([]byte("x"), 129),
		"pixels":    testPNG(t, 2, 2),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Replace(bytes.NewReader(value)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("replace error = %v", err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(directory, fileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("invalid replacement created current image: %v", err)
	}
}

func TestReplaceDoesNotDisturbCurrentImageOnFailure(t *testing.T) {
	directory := t.TempDir()
	store, err := New(directory, limits.Default().HostImage)
	if err != nil {
		t.Fatal(err)
	}
	original := testPNG(t, 1, 1)
	if _, err := store.Replace(bytes.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(bytes.NewReader([]byte("broken"))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid replacement error = %v", err)
	}
	file, _, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stored, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, original) {
		t.Fatal("failed replacement changed the current image")
	}
	replacement := testPNG(t, 2, 1)
	if _, err := store.Replace(bytes.NewReader(replacement)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		t.Fatalf("replacement retained unexpected files: %+v", entries)
	}
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	value.Set(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
