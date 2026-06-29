// Package binstore downloads and caches helper executables (reverse-ssh, bore)
// under the user's cache directory so the orchestrator works without the user
// installing them manually.
package binstore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// CacheDir returns the directory where helper binaries are cached,
// e.g. ~/.cache/runpod-orchestrator/bin.
func CacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runpod-orchestrator", "bin"), nil
}

// Ensure returns the path to a cached executable named name, downloading it from
// url (and marking it executable) if it is not already present.
func Ensure(ctx context.Context, name, url string) (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		return path, nil
	}

	if err := download(ctx, url, path); err != nil {
		return "", err
	}
	return path, nil
}

// EnsureTarGz returns the path to a cached executable named name, extracting it
// from the .tar.gz at url if not already present. member is the base name of the
// file to extract from the archive (e.g. "bore").
func EnsureTarGz(ctx context.Context, name, url, member string) (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		return path, nil
	}

	if err := extractTarGz(ctx, url, member, path); err != nil {
		return "", err
	}
	return path, nil
}

func extractTarGz(ctx context.Context, url, member, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("binstore: downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binstore: downloading %s: %s", url, resp.Status)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("binstore: gunzip %s: %w", url, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("binstore: %q not found in archive %s", member, url)
		}
		if err != nil {
			return fmt.Errorf("binstore: reading archive %s: %w", url, err)
		}
		if filepath.Base(hdr.Name) != member || hdr.Typeflag != tar.TypeReg {
			continue
		}

		tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if _, err := io.Copy(tmp, tr); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Chmod(tmpName, 0o755); err != nil {
			return err
		}
		return os.Rename(tmpName, dest)
	}
}

func download(ctx context.Context, url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("binstore: downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binstore: downloading %s: %s", url, resp.Status)
	}

	// Download to a temp file first so a partial download never looks complete.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("binstore: writing %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
