package cli

import (
	"encoding/json"
	"testing"
)

func nativeCheckpointRecordJSON(t *testing.T, image NativeCheckpointImage) map[string]any {
	t.Helper()
	var record checkpointRecord
	record.applyNativeImage(coordinatorImageFromNativeCheckpoint(image), true)
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Native map[string]any `json:"native"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Native
}

func TestNativeCheckpointRecordCarriesParentAndSizesWhenReported(t *testing.T) {
	native := nativeCheckpointRecordJSON(t, NativeCheckpointImage{
		ID: "chk_child", Provider: "p", Kind: "k",
		Bytes: 100, UniqueBytes: 100, ApparentBytes: 4096, ParentCheckpointID: "chk_parent",
	})
	if native["parentCheckpointId"] != "chk_parent" || native["uniqueBytes"] != float64(100) || native["apparentBytes"] != float64(4096) || native["bytes"] != float64(100) {
		t.Fatalf("native=%v", native)
	}
}

func TestNativeCheckpointRecordOmitsParentAndSizesWhenNotReported(t *testing.T) {
	native := nativeCheckpointRecordJSON(t, NativeCheckpointImage{ID: "chk_root", Provider: "p", Kind: "k", Bytes: 100})
	for _, key := range []string{"parentCheckpointId", "uniqueBytes", "apparentBytes"} {
		if _, ok := native[key]; ok {
			t.Fatalf("%s present in %v", key, native)
		}
	}
	if native["bytes"] != float64(100) {
		t.Fatalf("native=%v", native)
	}
}
