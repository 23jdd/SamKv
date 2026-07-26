package store

// 本文件验证 Manifest 世代名称稳定、严格解析并按数值 ID 而不是目录顺序排列。

import (
	"os"
	"reflect"
	"testing"
)

func TestManifestGenerationNameRoundTrip(t *testing.T) {
	name := manifestGenerationName(42)
	if name != "MANIFEST-00000000000000000042" {
		t.Fatalf("manifestGenerationName() = %q", name)
	}
	if id, ok := parseManifestGeneration(name); !ok || id != 42 {
		t.Fatalf("parseManifestGeneration(%q) = %d, %v", name, id, ok)
	}
}

func TestListManifestGenerationsIgnoresTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		manifestGenerationName(9),
		manifestGenerationName(2),
		"MANIFEST-1",
		manifestGenerationName(3) + ".tmp",
		currentFileName,
	} {
		if err := os.WriteFile(dir+"/"+name, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	generations, err := listManifestGenerations(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uint64
	for _, generation := range generations {
		ids = append(ids, generation.ID)
	}
	if !reflect.DeepEqual(ids, []uint64{2, 9}) {
		t.Fatalf("generation IDs = %v", ids)
	}
	next, err := nextManifestGeneration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if next != 10 {
		t.Fatalf("next generation = %d, want 10", next)
	}
}
