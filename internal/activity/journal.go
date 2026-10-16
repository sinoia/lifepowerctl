// Package activity stores bounded, human-readable operational history.
package activity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

const (
	DefaultSegmentBytes  = limits.DefaultActivitySegmentBytes
	DefaultMaxSegments   = limits.DefaultActivitySegments
	DefaultMaxEventBytes = limits.DefaultActivityEventBytes
)

type Options struct {
	SegmentBytes  int64
	MaxSegments   int
	MaxEventBytes int
}

type Event struct {
	ID      uint64          `json:"id"`
	Time    time.Time       `json:"time"`
	Type    string          `json:"type"`
	Actor   string          `json:"actor"`
	Subject string          `json:"subject,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

type Journal struct {
	mu          sync.Mutex
	dir         string
	options     Options
	segment     uint64
	nextEventID uint64
}

func (j *Journal) lockProcess(exclusive bool) (func(), error) {
	file, err := os.OpenFile(filepath.Join(j.dir, ".journal.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Activity lock: %w", err)
	}
	operation := syscall.LOCK_SH
	if exclusive {
		operation = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock Activity journal: %w", err)
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }, nil
}

func (j *Journal) refreshPosition(repairTail bool) error {
	segments, err := j.segmentNumbers()
	if err != nil {
		return err
	}
	j.segment, j.nextEventID = 1, 1
	var lastID uint64
	for index, number := range segments {
		segmentLastID, inspectErr := j.inspectSegment(number, repairTail && index == len(segments)-1)
		if inspectErr != nil {
			return inspectErr
		}
		if segmentLastID > lastID {
			lastID = segmentLastID
		}
	}
	if len(segments) > 0 {
		j.segment = segments[len(segments)-1]
	}
	j.nextEventID = lastID + 1
	return nil
}

func Open(dir string, options Options) (*Journal, error) {
	options = withDefaults(options)
	if options.SegmentBytes < 256 || options.MaxSegments < 2 || options.MaxEventBytes < 128 {
		return nil, fmt.Errorf("activity limits are too small")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create activity directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure activity directory: %w", err)
	}
	journal := &Journal{dir: dir, options: options, segment: 1, nextEventID: 1}
	unlock, err := journal.lockProcess(true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := journal.refreshPosition(true); err != nil {
		return nil, err
	}
	return journal, nil
}

func (j *Journal) Append(event Event) (Event, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	unlock, err := j.lockProcess(true)
	if err != nil {
		return Event{}, err
	}
	defer unlock()
	if err := j.refreshPosition(false); err != nil {
		return Event{}, err
	}
	if event.Type == "" || event.Actor == "" || event.Time.IsZero() {
		return Event{}, fmt.Errorf("activity type, actor, and time are required")
	}
	event.ID = j.nextEventID
	event.Time = event.Time.UTC()
	line, err := json.Marshal(event)
	if err != nil {
		return Event{}, fmt.Errorf("encode activity: %w", err)
	}
	line = append(line, '\n')
	if len(line) > j.options.MaxEventBytes {
		return Event{}, fmt.Errorf("activity is %d bytes; maximum is %d", len(line), j.options.MaxEventBytes)
	}
	path := j.segmentPath(j.segment)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 && info.Size()+int64(len(line)) > j.options.SegmentBytes {
		j.segment++
		path = j.segmentPath(j.segment)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Event{}, fmt.Errorf("inspect activity segment: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Event{}, fmt.Errorf("open activity segment: %w", err)
	}
	if _, err := file.Write(line); err != nil {
		_ = file.Close()
		return Event{}, fmt.Errorf("append activity: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return Event{}, fmt.Errorf("sync activity: %w", err)
	}
	if err := file.Close(); err != nil {
		return Event{}, fmt.Errorf("close activity: %w", err)
	}
	if err := syncDirectory(j.dir); err != nil {
		return Event{}, fmt.Errorf("sync activity directory: %w", err)
	}
	j.nextEventID++
	if err := j.enforceRetention(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (j *Journal) Events() ([]Event, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	unlock, err := j.lockProcess(false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	segments, err := j.segmentNumbers()
	if err != nil {
		return nil, err
	}
	var events []Event
	for _, number := range segments {
		file, err := os.Open(j.segmentPath(number))
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024), j.options.MaxEventBytes)
		for scanner.Scan() {
			var event Event
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("decode activity segment %d: %w", number, err)
			}
			events = append(events, event)
		}
		if err := scanner.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	return events, nil
}

func (j *Journal) inspectSegment(number uint64, repairTail bool) (uint64, error) {
	path := j.segmentPath(number)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if !repairTail {
			return 0, fmt.Errorf("activity segment %d has a truncated record", number)
		}
		end := bytes.LastIndexByte(data, '\n') + 1
		if err := os.Truncate(path, int64(end)); err != nil {
			return 0, fmt.Errorf("repair activity segment %d: %w", number, err)
		}
		data = data[:end]
	}
	var lastID uint64
	reader := bufio.NewReader(bytes.NewReader(data))
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(line) == 0 {
			break
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		var event Event
		if decodeErr := json.Unmarshal(bytes.TrimSpace(line), &event); decodeErr != nil || event.ID == 0 {
			return 0, fmt.Errorf("decode activity segment %d: invalid complete record", number)
		}
		if event.ID <= lastID {
			return 0, fmt.Errorf("activity segment %d has non-increasing IDs", number)
		}
		lastID = event.ID
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return lastID, nil
}

func (j *Journal) enforceRetention() error {
	segments, err := j.segmentNumbers()
	if err != nil {
		return err
	}
	removeCount := len(segments) - j.options.MaxSegments
	for index := 0; index < removeCount; index++ {
		if err := os.Remove(j.segmentPath(segments[index])); err != nil {
			return fmt.Errorf("remove expired activity segment: %w", err)
		}
	}
	if removeCount > 0 {
		return syncDirectory(j.dir)
	}
	return nil
}

func (j *Journal) segmentNumbers() ([]uint64, error) {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, err
	}
	var numbers []uint64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "activity-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		numberText := strings.TrimSuffix(strings.TrimPrefix(name, "activity-"), ".jsonl")
		number, err := strconv.ParseUint(numberText, 10, 64)
		if err != nil || number == 0 {
			return nil, fmt.Errorf("invalid activity segment name %q", name)
		}
		numbers = append(numbers, number)
	}
	sort.Slice(numbers, func(i, k int) bool { return numbers[i] < numbers[k] })
	return numbers, nil
}

func (j *Journal) segmentPath(number uint64) string {
	return filepath.Join(j.dir, fmt.Sprintf("activity-%06d.jsonl", number))
}

func withDefaults(options Options) Options {
	if options.SegmentBytes == 0 {
		options.SegmentBytes = DefaultSegmentBytes
	}
	if options.MaxSegments == 0 {
		options.MaxSegments = DefaultMaxSegments
	}
	if options.MaxEventBytes == 0 {
		options.MaxEventBytes = DefaultMaxEventBytes
	}
	return options
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
