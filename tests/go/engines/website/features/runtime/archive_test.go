package runtime_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func assertArchivePreflightRejectsUnsafePackages(t *testing.T) {
	limits := runtime.ArchiveLimits{
		MaxArchiveBytes: 1 << 20, MaxEntries: 32, MaxEntryBytes: 4096,
		MaxExpandedBytes: 8192, MaxCompressionRatio: 20, MaxManifestBytes: 1 << 20,
	}
	valid := zipBytes(t, map[string]string{
		"manifest.json":  string(mustValidManifest(t)),
		"assets/app.js":  "export function render() {}",
		"assets/app.css": "body{}",
	})
	retiredABI := strings.Replace(string(mustValidManifest(t)), `"abi": "static-lifecycle"`, `"abi": "static-lifecycle-v2"`, 1)

	cases := []struct {
		name  string
		bytes []byte
		want  error
	}{
		{"malformed", []byte("not a zip"), runtime.ErrArchive},
		{"traversal", zipBytes(t, map[string]string{"manifest.json": string(mustValidManifest(t)), "../outside.js": "x"}), runtime.ErrArchive},
		{"windows path", zipBytes(t, map[string]string{"manifest.json": string(mustValidManifest(t)), "C:\\outside.js": "x"}), runtime.ErrArchive},
		{"symlink", zipWithHeader(t, "assets/link.js", os.ModeSymlink|0o777, "assets/app.js"), runtime.ErrArchive},
		{"duplicate normalized path", duplicateEntryZip(t), runtime.ErrArchive},
		{"archive bomb", zipBytes(t, map[string]string{"manifest.json": string(mustValidManifest(t)), "assets/bomb.js": strings.Repeat("A", 40000)}), runtime.ErrArchiveLimit},
		{"missing manifest", zipBytes(t, map[string]string{"assets/app.js": "x"}), runtime.ErrArchive},
		{"retired ABI", zipBytes(t, map[string]string{"manifest.json": retiredABI, "assets/app.js": "x", "assets/app.css": "x"}), runtime.ErrABI},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			files, manifest, err := runtime.PreflightArchive(testCase.bytes, limits)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("err=%v, want %v", err, testCase.want)
			}
			if len(files) != 0 || manifest.Key != "" {
				t.Fatalf("preflight returned partial output: files=%d manifest=%+v", len(files), manifest)
			}
		})
	}

	files, manifest, err := runtime.PreflightArchive(valid, limits)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Runtime.ABI != runtime.ABI || len(files) != 3 || files[0].Path != "assets/app.css" {
		t.Fatalf("unexpected preflight result: manifest=%+v files=%+v", manifest, files)
	}
}

func TestArchivePreflightRejectsUnsafePackages(t *testing.T) {
	assertArchivePreflightRejectsUnsafePackages(t)
}

func web011FeatureArchiveRejectsUnsafePackages(t *testing.T) {
	assertArchivePreflightRejectsUnsafePackages(t)
}

func web024RuntimePreflightRejectsUnsafePackages(t *testing.T) {
	assertArchivePreflightRejectsUnsafePackages(t)
}
