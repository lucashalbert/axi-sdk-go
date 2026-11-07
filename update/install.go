package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	maxArchiveBytes = 256 << 20
	maxBinaryBytes  = 512 << 20
	maxSumsBytes    = 1 << 20
)

// InstallConfig configures Install and Update.
type InstallConfig struct {
	Repo    string       // "owner/name" on GitHub
	Binary  string       // executable name inside the archive, without ".exe"
	Current string       // running version; a non-semver value such as "dev" always updates
	Client  *http.Client // nil means http.DefaultClient
}

// Install replaces the running executable with the latest GitHub release if
// that release is newer than c.Current. See Update for the safety contract.
func Install(ctx context.Context, c InstallConfig) error {
	_, _, err := Update(ctx, c)
	return err
}

// Update is Install that also reports the latest version (without a leading
// "v") and whether the executable was replaced.
//
// The archive for the running GOOS/GOARCH is downloaded and its SHA-256 is
// compared with the release's checksums.txt before anything else happens.
// The running binary is touched only by the final rename, so any failure
// before it (network, truncation, checksum mismatch, bad archive,
// cancellation) leaves it byte-identical.
func Update(ctx context.Context, c InstallConfig) (latest string, updated bool, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", false, fmt.Errorf("locate running executable: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return update(ctx, c, exe, runtime.GOOS, runtime.GOARCH)
}

func update(ctx context.Context, c InstallConfig, target, goos, goarch string) (string, bool, error) {
	rel, err := fetchLatest(ctx, c.Client, c.Repo, "axi-update/"+bare(c.Current))
	if err != nil {
		return "", false, fmt.Errorf("find latest release: %w", err)
	}
	latest := bare(rel.TagName)
	if semverValid(c.Current) && !isNewer(rel.TagName, c.Current) {
		return latest, false, nil
	}
	arc, err := pickArchive(rel.Assets, c.Binary, goos, goarch)
	if err != nil {
		return latest, false, err
	}
	sumsAsset, err := pickChecksums(rel.Assets)
	if err != nil {
		return latest, false, err
	}
	var sums strings.Builder
	if _, err := fetch(ctx, c.Client, sumsAsset.URL, &sums, maxSumsBytes); err != nil {
		return latest, false, fmt.Errorf("download checksums: %w", err)
	}
	want, err := checksumFor([]byte(sums.String()), arc.Name)
	if err != nil {
		return latest, false, err
	}

	tmpArc, err := os.CreateTemp("", "axi-update-*")
	if err != nil {
		return latest, false, err
	}
	defer func() { _ = os.Remove(tmpArc.Name()) }()
	defer func() { _ = tmpArc.Close() }()
	got, err := fetch(ctx, c.Client, arc.URL, tmpArc, maxArchiveBytes)
	if err != nil {
		return latest, false, fmt.Errorf("download %s: %w", arc.Name, err)
	}
	if got != want {
		return latest, false, fmt.Errorf("checksum mismatch for %s: expected %s, got %s; binary left unchanged", arc.Name, want, got)
	}
	if err := ctx.Err(); err != nil {
		return latest, false, err
	}

	newBin, err := extractBinary(tmpArc, arc.Name, c.Binary, filepath.Dir(target), target)
	if err != nil {
		return latest, false, err
	}
	defer func() { _ = os.Remove(newBin) }() // no-op after a successful rename
	if err := ctx.Err(); err != nil {
		return latest, false, err
	}
	if err := replaceFile(target, newBin, goos == "windows"); err != nil {
		return latest, false, fmt.Errorf("replace %s: %w", target, err)
	}
	return latest, true, nil
}

func semverValid(v string) bool { return semver.IsValid(canonical(v)) }

// fetch streams url into w and returns the SHA-256 of what was written.
func fetch(ctx context.Context, client *http.Client, url string, w io.Writer, limit int64) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s", resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("download exceeds %d bytes", limit)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary copies the executable out of the archive into a new file next
// to target (so the final rename stays on one filesystem) and returns its path.
// Entry names are only compared, never used as paths.
func extractBinary(arc *os.File, archiveName, binary, dir, target string) (string, error) {
	if _, err := arc.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	mode := os.FileMode(0o755)
	if st, err := os.Stat(target); err == nil {
		mode = st.Mode().Perm() | 0o111
	}
	out, err := os.CreateTemp(dir, "."+filepath.Base(target)+".new-*")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return "", err
	}
	var found bool
	if strings.HasSuffix(strings.ToLower(archiveName), ".zip") {
		found, err = copyFromZip(arc, binary, out)
	} else {
		found, err = copyFromTarGz(arc, binary, out)
	}
	if err != nil {
		return fail(fmt.Errorf("read %s: %w", archiveName, err))
	}
	if !found {
		return fail(fmt.Errorf("%s does not contain %s", archiveName, binary))
	}
	if err := out.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

func isBinaryName(entry, binary string) bool {
	base := filepath.Base(strings.ReplaceAll(entry, "\\", "/"))
	return base == binary || base == binary+".exe"
}

func copyCapped(dst io.Writer, src io.Reader) error {
	n, err := io.Copy(dst, io.LimitReader(src, maxBinaryBytes+1))
	if err == nil && n > maxBinaryBytes {
		err = errors.New("binary exceeds size limit")
	}
	return err
}

func copyFromTarGz(arc *os.File, binary string, dst io.Writer) (bool, error) {
	gz, err := gzip.NewReader(arc)
	if err != nil {
		return false, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if h.Typeflag == tar.TypeReg && isBinaryName(h.Name, binary) {
			return true, copyCapped(dst, tr)
		}
	}
}

func copyFromZip(arc *os.File, binary string, dst io.Writer) (bool, error) {
	st, err := arc.Stat()
	if err != nil {
		return false, err
	}
	zr, err := zip.NewReader(arc, st.Size())
	if err != nil {
		return false, err
	}
	for _, f := range zr.File {
		if f.FileInfo().Mode().IsRegular() && isBinaryName(f.Name, binary) {
			rc, err := f.Open()
			if err != nil {
				return false, err
			}
			defer func() { _ = rc.Close() }()
			return true, copyCapped(dst, rc)
		}
	}
	return false, nil
}

// replaceFile swaps newBin into place at target.
//
// Elsewhere a rename over the running executable is atomic. Windows refuses
// to overwrite a running executable but allows renaming it, so with aside
// set the current file is moved to target+".old" first and put back if the
// second rename fails.
func replaceFile(target, newBin string, aside bool) error {
	if !aside {
		return os.Rename(newBin, target)
	}
	old := target + ".old"
	_ = os.Remove(old) // a previous update's leftover; fails harmlessly while still running
	if err := os.Rename(target, old); err != nil {
		return err
	}
	if err := os.Rename(newBin, target); err != nil {
		if rerr := os.Rename(old, target); rerr != nil {
			return fmt.Errorf("%w (and restoring the old binary failed: %w)", err, rerr)
		}
		return err
	}
	return nil
}
