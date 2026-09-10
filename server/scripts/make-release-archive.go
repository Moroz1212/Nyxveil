//go:build ignore

// Create tar metadata from the release contract, independent of host OS modes.
package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := flag.String("dir", "", "release architecture directory")
	out := flag.String("out", "", "output tar.gz")
	flag.Parse()
	if err := pack(*dir, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func pack(dir, out string) error {
	if dir == "" || out == "" {
		return fmt.Errorf("dir and out required")
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	z := gzip.NewWriter(f)
	t := tar.NewWriter(z)
	err = filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported archive entry: %s", path)
		}
		rel, err := filepath.Rel(filepath.Dir(dir), path)
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "root", "root"
		h.Mode = 0o644
		base := filepath.Base(path)
		if info.IsDir() || strings.HasSuffix(base, ".sh") || base == "nyxveil-server" || base == "nyxveilctl" || base == "nyxveil-catalog-verify" {
			h.Mode = 0o755
		}
		if err := t.WriteHeader(h); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(t, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	if err := t.Close(); err != nil {
		return err
	}
	if err := z.Close(); err != nil {
		return err
	}
	return f.Sync()
}
