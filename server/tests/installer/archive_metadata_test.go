package installer_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestArchiveModesIndependentOfHost(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "linux-amd64")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"nyxveil-server": 0755, "nyxveilctl": 0755, "nyxveil-catalog-verify": 0755, "install.sh": 0755, "production-gate.sh": 0755, "unit.service": 0644, "VERSION": 0644, "manifest.json": 0644}
	for name := range want {
		if err := os.WriteFile(filepath.Join(src, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "candidate.tar.gz")
	cmd := exec.Command("go", "run", filepath.Join(findServerRoot(t), "scripts", "make-release-archive.go"), "-dir", src, "-out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	r := tar.NewReader(z)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		name := filepath.Base(h.Name)
		mode, ok := want[name]
		if !ok || h.Mode != mode {
			t.Fatalf("unexpected mode/name: %s %o", name, h.Mode)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing entries: %v", want)
	}
}
