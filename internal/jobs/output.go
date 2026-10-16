package jobs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

type OutputStream string

const (
	OutputStdout OutputStream = "stdout"
	OutputStderr OutputStream = "stderr"
)

var ErrOutputNotFound = errors.New("job output not found")

type OutputStore struct {
	directory string
	limits    limits.Jobs
}

// NewOutputStore is used by the selected-user runner and creates its bounded
// output directory. Packaging supplies the shared group on the parent.
func NewOutputStore(directory string, jobLimits limits.Jobs) (*OutputStore, error) {
	store, err := newOutputStore(directory, jobLimits)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0750); err != nil {
		return nil, fmt.Errorf("create job output directory: %w", err)
	}
	return store, nil
}

// NewOutputReader validates configuration without creating runner-owned paths.
func NewOutputReader(directory string, jobLimits limits.Jobs) (*OutputStore, error) {
	return newOutputStore(directory, jobLimits)
}

func newOutputStore(directory string, jobLimits limits.Jobs) (*OutputStore, error) {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || jobLimits.MaximumStreamBytes <= 0 || jobLimits.MaxRetainedOutputBytes <= 0 || jobLimits.RetainedRunsTotal < 1 {
		return nil, fmt.Errorf("invalid job output store configuration")
	}
	return &OutputStore{directory: directory, limits: jobLimits}, nil
}

func (store *OutputStore) Save(runID string, stdout, stderr []byte) error {
	if !validRunID(runID) || int64(len(stdout)) > store.limits.MaximumStreamBytes || int64(len(stderr)) > store.limits.MaximumStreamBytes {
		return fmt.Errorf("invalid bounded job output")
	}
	if err := store.write(runID, OutputStdout, stdout); err != nil {
		return err
	}
	if err := store.write(runID, OutputStderr, stderr); err != nil {
		return err
	}
	return store.prune(runID)
}

func (store *OutputStore) Read(runID string, stream OutputStream) ([]byte, error) {
	path, err := store.path(runID, stream)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrOutputNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open job output: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > store.limits.MaximumStreamBytes {
		return nil, fmt.Errorf("invalid job output file")
	}
	data, err := io.ReadAll(io.LimitReader(file, store.limits.MaximumStreamBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read job output: %w", err)
	}
	if int64(len(data)) > store.limits.MaximumStreamBytes {
		return nil, fmt.Errorf("job output exceeds limit")
	}
	return data, nil
}

func (store *OutputStore) Exists(runID string, stream OutputStream) (bool, error) {
	path, err := store.path(runID, stream)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("output path is not a regular file")
	}
	return true, nil
}

func (store *OutputStore) write(runID string, stream OutputStream, data []byte) error {
	path, err := store.path(runID, stream)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(store.directory, ".output-*")
	if err != nil {
		return fmt.Errorf("create temporary job output: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0640); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write job output: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync job output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close job output: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish job output: %w", err)
	}
	return nil
}

func (store *OutputStore) path(runID string, stream OutputStream) (string, error) {
	if !validRunID(runID) || stream != OutputStdout && stream != OutputStderr {
		return "", fmt.Errorf("invalid job output identity")
	}
	return filepath.Join(store.directory, runID+"."+string(stream)), nil
}

type retainedOutput struct {
	runID string
	mtime int64
	size  int64
}

func (store *OutputStore) prune(preserve string) error {
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return fmt.Errorf("list job output: %w", err)
	}
	runs := make(map[string]*retainedOutput)
	for _, entry := range entries {
		name := entry.Name()
		base, suffix, found := strings.Cut(name, ".")
		if !found || !validRunID(base) || suffix != string(OutputStdout) && suffix != string(OutputStderr) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		run := runs[base]
		if run == nil {
			run = &retainedOutput{runID: base}
			runs[base] = run
		}
		run.size += info.Size()
		if modified := info.ModTime().UnixNano(); modified > run.mtime {
			run.mtime = modified
		}
	}
	ordered := make([]*retainedOutput, 0, len(runs))
	var total int64
	for _, run := range runs {
		ordered = append(ordered, run)
		total += run.size
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].mtime < ordered[j].mtime })
	for len(ordered) > store.limits.RetainedRunsTotal || total > store.limits.MaxRetainedOutputBytes {
		index := 0
		if ordered[index].runID == preserve && len(ordered) > 1 {
			index = 1
		}
		victim := ordered[index]
		for _, stream := range []OutputStream{OutputStdout, OutputStderr} {
			path, _ := store.path(victim.runID, stream)
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove retained job output: %w", err)
			}
		}
		total -= victim.size
		ordered = append(ordered[:index], ordered[index+1:]...)
		if len(ordered) == 1 && ordered[0].runID == preserve && total > store.limits.MaxRetainedOutputBytes {
			break
		}
	}
	return nil
}
