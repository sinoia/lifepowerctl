package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testState struct {
	Value string `json:"value"`
}

func TestStoreSaveLoadAndRevision(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, "controller")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(0, testState{Value: "one"})
	if err != nil || saved.Revision != 1 {
		t.Fatalf("Save() = %+v, %v", saved, err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Revision != 1 || loaded.Recovered {
		t.Fatalf("Load() = %+v, %v", loaded, err)
	}
	var value testState
	if err := json.Unmarshal(loaded.Payload, &value); err != nil || value.Value != "one" {
		t.Fatalf("payload = %+v, %v", value, err)
	}
	if _, err := store.Save(0, testState{Value: "stale"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale Save() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "controller.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions = %v, %v", info.Mode().Perm(), err)
	}
}

func TestStoreRecoversPreviousSnapshot(t *testing.T) {
	dir := t.TempDir()
	store, _ := New(dir, "controller")
	first, _ := store.Save(0, testState{Value: "one"})
	if _, err := store.Save(first.Revision, testState{Value: "two"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "controller.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || !loaded.Recovered || loaded.Revision != 1 {
		t.Fatalf("recovered Load() = %+v, %v", loaded, err)
	}
}

func TestStoreRejectsFutureSchema(t *testing.T) {
	dir := t.TempDir()
	store, _ := New(dir, "controller")
	data := []byte(`{"schema":2,"revision":1,"payload":{"value":"future"}}`)
	if err := os.WriteFile(filepath.Join(dir, "controller.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestStoreRejectsOversizedSnapshot(t *testing.T) {
	store, _ := New(t.TempDir(), "controller")
	if _, err := store.Save(0, testState{Value: strings.Repeat("x", MaxSnapshotBytes)}); err == nil {
		t.Fatal("Save accepted an oversized encoded snapshot")
	}
}

func BenchmarkStoreSave(b *testing.B) {
	// Leave room for JSON field and string encoding beneath the 256 KiB
	// encoded-payload limit. The previous 256 KiB and 1 MiB cases measured
	// payloads that Save now correctly rejects.
	for _, size := range []int{32, 128 << 10, 240 << 10} {
		b.Run(fmt.Sprintf("%d-bytes", size), func(b *testing.B) {
			store, _ := New(b.TempDir(), "controller")
			value := testState{Value: strings.Repeat("x", size)}
			var revision uint64
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				saved, err := store.Save(revision, value)
				if err != nil {
					b.Fatal(err)
				}
				revision = saved.Revision
			}
		})
	}
}
