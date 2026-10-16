// Package storage provides crash-conscious persistence for low-write-rate
// controller state. Large job output is intentionally outside this store.
package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

var (
	ErrNotFound         = errors.New("state not found")
	ErrRevisionConflict = errors.New("state revision conflict")
	ErrFutureSchema     = errors.New("state uses a newer schema")
)

const MinimumReadableSchema = 1
const CurrentSchema = 1
const MaxSnapshotBytes = limits.DefaultMaxSnapshotBytes

type envelope struct {
	Schema   int             `json:"schema"`
	Revision uint64          `json:"revision"`
	Payload  json.RawMessage `json:"payload"`
}

type Snapshot struct {
	Schema    int
	Revision  uint64
	Payload   json.RawMessage
	Recovered bool
}

// Store has one in-process writer. The service privilege model must ensure
// that only one controller process owns its directory.
type Store struct {
	mu   sync.Mutex
	dir  string
	name string
	mode fs.FileMode
}

func New(dir, name string) (*Store, error) {
	return NewWithDirectoryMode(dir, name, 0o700)
}

// NewWithDirectoryMode permits a caller to retain a deliberately shared
// directory boundary while snapshot files remain private (0600). The mode
// must not grant any access to other users.
func NewWithDirectoryMode(dir, name string, mode fs.FileMode) (*Store, error) {
	if !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return nil, fmt.Errorf("invalid state name %q", name)
	}
	if mode&^(fs.ModePerm|fs.ModeSetgid) != 0 || mode&0o007 != 0 || mode&0o700 != 0o700 {
		return nil, fmt.Errorf("invalid state directory mode %04o", mode)
	}
	return &Store{dir: dir, name: name, mode: mode}, nil
}

func (s *Store) Load() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) Save(expectedRevision uint64, value any) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.loadLocked()
	switch {
	case err == nil && current.Revision != expectedRevision:
		return Snapshot{}, fmt.Errorf("%w: have %d, expected %d", ErrRevisionConflict, current.Revision, expectedRevision)
	case errors.Is(err, ErrNotFound) && expectedRevision != 0:
		return Snapshot{}, fmt.Errorf("%w: state does not exist", ErrRevisionConflict)
	case err != nil && !errors.Is(err, ErrNotFound):
		return Snapshot{}, err
	}
	if current.Recovered {
		if err := s.restoreBackup(); err != nil {
			return Snapshot{}, err
		}
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode payload: %w", err)
	}
	if len(payload) > MaxSnapshotBytes {
		return Snapshot{}, fmt.Errorf("encoded payload is %d bytes; maximum is %d", len(payload), MaxSnapshotBytes)
	}
	next := envelope{Schema: CurrentSchema, Revision: expectedRevision + 1, Payload: payload}
	encoded, err := json.Marshal(next)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode envelope: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := s.replace(encoded); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Schema: next.Schema, Revision: next.Revision, Payload: bytes.Clone(payload)}, nil
}

func (s *Store) loadLocked() (Snapshot, error) {
	current, currentErr := readSnapshot(s.currentPath())
	if currentErr == nil {
		return current, nil
	}
	backup, backupErr := readSnapshot(s.backupPath())
	if backupErr == nil {
		backup.Recovered = true
		return backup, nil
	}
	if errors.Is(currentErr, fs.ErrNotExist) && errors.Is(backupErr, fs.ErrNotExist) {
		return Snapshot{}, ErrNotFound
	}
	if errors.Is(backupErr, fs.ErrNotExist) {
		return Snapshot{}, fmt.Errorf("load current: %w", currentErr)
	}
	return Snapshot{}, fmt.Errorf("load current: %v; load backup: %v", currentErr, backupErr)
}

func readSnapshot(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored envelope
	if err := decoder.Decode(&stored); err != nil {
		return Snapshot{}, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Snapshot{}, fmt.Errorf("decode %s: trailing data", filepath.Base(path))
	}
	if stored.Schema > CurrentSchema {
		return Snapshot{}, fmt.Errorf("%w: %d", ErrFutureSchema, stored.Schema)
	}
	if stored.Schema < MinimumReadableSchema || stored.Revision == 0 || len(stored.Payload) == 0 {
		return Snapshot{}, fmt.Errorf("decode %s: invalid envelope", filepath.Base(path))
	}
	return Snapshot{Schema: stored.Schema, Revision: stored.Revision, Payload: bytes.Clone(stored.Payload)}, nil
}

func (s *Store) replace(data []byte) (returnErr error) {
	if err := os.MkdirAll(s.dir, s.mode); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(s.dir, s.mode); err != nil {
		return fmt.Errorf("secure state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.dir, "."+s.name+"-*")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary state: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}

	currentPath := s.currentPath()
	backupPath := s.backupPath()
	if err := os.Remove(backupPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove old backup: %w", err)
	}
	if err := os.Rename(currentPath, backupPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("rotate current state: %w", err)
	}
	if err := os.Rename(temporaryPath, currentPath); err != nil {
		_ = os.Rename(backupPath, currentPath)
		return fmt.Errorf("install state: %w", err)
	}
	if err := syncDirectory(s.dir); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}

func (s *Store) restoreBackup() error {
	if err := os.Remove(s.currentPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove damaged current state: %w", err)
	}
	if err := os.Rename(s.backupPath(), s.currentPath()); err != nil {
		return fmt.Errorf("restore previous state: %w", err)
	}
	if err := syncDirectory(s.dir); err != nil {
		return fmt.Errorf("sync restored state: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *Store) currentPath() string { return filepath.Join(s.dir, s.name+".json") }
func (s *Store) backupPath() string  { return filepath.Join(s.dir, s.name+".previous.json") }
