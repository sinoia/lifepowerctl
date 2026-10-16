package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalAppendRotateAndRetain(t *testing.T) {
	dir := t.TempDir()
	journal, err := Open(dir, Options{SegmentBytes: 256, MaxSegments: 2, MaxEventBytes: 512})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		_, err := journal.Append(Event{Time: time.Unix(int64(index+1), 0), Type: "test", Actor: "tester", Details: []byte(`{"value":"` + strings.Repeat("x", 40) + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	segments, err := journal.segmentNumbers()
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 {
		t.Fatalf("segment count = %d, want 2", len(segments))
	}
	events, err := journal.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[len(events)-1].ID != 8 {
		t.Fatalf("retained events = %+v", events)
	}
}

func TestJournalRepairsTruncatedLastRecord(t *testing.T) {
	dir := t.TempDir()
	journal, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := journal.Append(Event{Time: time.Unix(1, 0), Type: "test", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "activity-000001.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"id":2,"time":`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	reopened, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := reopened.Append(Event{Time: time.Unix(2, 0), Type: "test", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != 1 || second.ID != 2 {
		t.Fatalf("event IDs = %d, %d", first.ID, second.ID)
	}
	events, err := reopened.Events()
	if err != nil || len(events) != 2 {
		t.Fatalf("Events() = %+v, %v", events, err)
	}
}

func TestSeparateJournalHandlesRefreshEventIdentity(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	one, err := first.Append(Event{Time: time.Unix(1, 0), Type: "first", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.Append(Event{Time: time.Unix(2, 0), Type: "second", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	three, err := first.Append(Event{Time: time.Unix(3, 0), Type: "third", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != 1 || two.ID != 2 || three.ID != 3 {
		t.Fatalf("event IDs = %d, %d, %d", one.ID, two.ID, three.ID)
	}
	events, err := second.Events()
	if err != nil || len(events) != 3 {
		t.Fatalf("Events() = %+v, %v", events, err)
	}
}

func TestJournalRejectsOversizedEvent(t *testing.T) {
	journal, err := Open(t.TempDir(), Options{MaxEventBytes: 256})
	if err != nil {
		t.Fatal(err)
	}
	_, err = journal.Append(Event{Time: time.Now(), Type: "test", Actor: "tester", Subject: strings.Repeat("x", 512)})
	if err == nil {
		t.Fatal("Append accepted an oversized event")
	}
}

func BenchmarkJournalAppend(b *testing.B) {
	journal, err := Open(b.TempDir(), Options{})
	if err != nil {
		b.Fatal(err)
	}
	details := []byte(`{"value":"` + strings.Repeat("x", 512) + `"}`)
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := journal.Append(Event{
			Time:    time.Unix(int64(index+1), 0),
			Type:    "benchmark",
			Actor:   "tester",
			Details: details,
		}); err != nil {
			b.Fatal(err)
		}
	}
}
