package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncProjectionBoundsConcurrentMetadata(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sample"), "payload")
	info, err := os.Lstat(filepath.Join(root, "sample"))
	if err != nil {
		t.Fatal(err)
	}
	managed, err := newManagedSyncScope(root)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := range 40 {
		paths = append(paths, fmt.Sprintf("file%02d", i))
	}
	var active, maximum atomic.Int32
	ready := make(chan struct{})
	var release sync.Once
	stat := func(string) (os.FileInfo, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		if n == 4 {
			release.Do(func() { close(ready) })
		}
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			return nil, errors.New("metadata reads did not overlap")
		}
		return info, nil
	}
	manifest, _, err := projectSyncManifestWithStat(root, SyncExcludeRules{}, nil, paths, syncManifestScope{}, managed, stat)
	if err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 4 || active.Load() != 0 {
		t.Fatalf("metadata concurrency: max=%d active=%d", maximum.Load(), active.Load())
	}
	if !reflect.DeepEqual(manifest.Files, paths) || manifest.Bytes != int64(len(paths))*info.Size() {
		t.Fatalf("projection: %+v", manifest)
	}
}

func TestSyncProjectionKeepsScopeOrderingAndMembership(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"b", "a", "excluded", "keep/file"} {
		writeFile(t, filepath.Join(root, name), name)
	}
	if err := os.Symlink("missing", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	managed, err := newManagedSyncScope(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"b", "a", "b", "excluded", "missing", "keep", "link", "keep/file", "../escape", "gitlink"}
	rules := newSyncExcludeRules([]string{"excluded", "keep", "!keep/file"}, syncExcludeConfigured)
	scope := syncManifestScope{gitlinkPaths: map[string]struct{}{"gitlink": {}}}
	for range 5 {
		manifest, seen, err := projectSyncManifest(root, rules, nil, paths, scope, managed)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"a", "b", "keep/file", "link"}
		if !reflect.DeepEqual(manifest.Files, want) || len(seen) != len(want) {
			t.Fatalf("membership changed: %+v %v", manifest, seen)
		}
		var size int64
		for _, rel := range want {
			info, err := os.Lstat(filepath.Join(root, rel))
			if err != nil {
				t.Fatal(err)
			}
			size += info.Size()
		}
		if manifest.Bytes != size {
			t.Fatalf("bytes=%d want=%d", manifest.Bytes, size)
		}
	}
}
