// Package hostimage owns the single bounded presentation image for a host.
package hostimage

import (
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

var (
	ErrNotFound = errors.New("host image not found")
	ErrInvalid  = errors.New("invalid host image")
)

const fileName = "host-image"

type Info struct {
	ContentType string
	Width       int
	Height      int
	Size        int64
	Modified    int64
}

type Store struct {
	directory string
	path      string
	limits    limits.HostImage
}

func New(directory string, imageLimits limits.HostImage) (*Store, error) {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, fmt.Errorf("host image directory must be a clean absolute path")
	}
	if imageLimits.MaxBytes <= 0 || imageLimits.MaxPixels <= 0 {
		return nil, fmt.Errorf("host image limits must be positive")
	}
	return &Store{directory: directory, path: filepath.Join(directory, fileName), limits: imageLimits}, nil
}

func (store *Store) Limits() limits.HostImage { return store.limits }

func (store *Store) Open() (*os.File, Info, error) {
	file, err := os.Open(store.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, fmt.Errorf("open host image: %w", err)
	}
	info, err := store.inspect(file, false)
	if err != nil {
		_ = file.Close()
		return nil, Info{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, Info{}, fmt.Errorf("rewind host image: %w", err)
	}
	return file, info, nil
}

func (store *Store) Replace(source io.Reader) (Info, error) {
	if source == nil {
		return Info{}, fmt.Errorf("%w: image data is required", ErrInvalid)
	}
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return Info{}, fmt.Errorf("create host image directory: %w", err)
	}
	temporary, err := os.CreateTemp(store.directory, ".host-image-*")
	if err != nil {
		return Info{}, fmt.Errorf("create temporary host image: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return Info{}, fmt.Errorf("secure temporary host image: %w", err)
	}
	written, err := io.Copy(temporary, io.LimitReader(source, store.limits.MaxBytes+1))
	if err != nil {
		return Info{}, fmt.Errorf("write temporary host image: %w", err)
	}
	if written == 0 || written > store.limits.MaxBytes {
		return Info{}, fmt.Errorf("%w: image must be between 1 and %d bytes", ErrInvalid, store.limits.MaxBytes)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return Info{}, fmt.Errorf("rewind temporary host image: %w", err)
	}
	info, err := store.inspect(temporary, true)
	if err != nil {
		return Info{}, err
	}
	if err := temporary.Sync(); err != nil {
		return Info{}, fmt.Errorf("sync temporary host image: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Info{}, fmt.Errorf("close temporary host image: %w", err)
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return Info{}, fmt.Errorf("install host image: %w", err)
	}
	if err := syncDirectory(store.directory); err != nil {
		return Info{}, fmt.Errorf("sync host image directory: %w", err)
	}
	return info, nil
}

func (store *Store) Remove() error {
	err := os.Remove(store.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove host image: %w", err)
	}
	if err := syncDirectory(store.directory); err != nil {
		return fmt.Errorf("sync host image directory: %w", err)
	}
	return nil
}

func (store *Store) inspect(file *os.File, decode bool) (Info, error) {
	stat, err := file.Stat()
	if err != nil {
		return Info{}, fmt.Errorf("inspect host image: %w", err)
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > store.limits.MaxBytes {
		return Info{}, fmt.Errorf("%w: image file has an invalid type or size", ErrInvalid)
	}
	header := make([]byte, 512)
	count, readErr := io.ReadFull(file, header)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return Info{}, fmt.Errorf("%w: read image header: %v", ErrInvalid, readErr)
	}
	contentType := http.DetectContentType(header[:count])
	if contentType != "image/jpeg" && contentType != "image/png" {
		return Info{}, fmt.Errorf("%w: only JPEG and PNG images are supported", ErrInvalid)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Info{}, fmt.Errorf("rewind host image: %w", err)
	}
	configuration, format, err := image.DecodeConfig(file)
	if err != nil || format != "jpeg" && format != "png" || contentType != "image/"+format {
		return Info{}, fmt.Errorf("%w: image data could not be decoded", ErrInvalid)
	}
	if configuration.Width <= 0 || configuration.Height <= 0 || int64(configuration.Width) > store.limits.MaxPixels/int64(configuration.Height) {
		return Info{}, fmt.Errorf("%w: image exceeds the %d pixel limit", ErrInvalid, store.limits.MaxPixels)
	}
	if decode {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return Info{}, fmt.Errorf("rewind host image: %w", err)
		}
		if _, decodedFormat, err := image.Decode(file); err != nil || decodedFormat != format {
			return Info{}, fmt.Errorf("%w: image data is incomplete or malformed", ErrInvalid)
		}
	}
	return Info{ContentType: contentType, Width: configuration.Width, Height: configuration.Height, Size: stat.Size(), Modified: stat.ModTime().UnixNano()}, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
