package expr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
)

// hashFiles() is the one GH built-in that intentionally touches the
// filesystem. It resolves each pattern against the workspace root
// (the process working directory, which the scheduler will set to
// $GITHUB_WORKSPACE for real runs and tests scope via t.Chdir),
// gathers all matching files, deduplicates them, sorts by relative
// POSIX path, and folds them into a single SHA-256 digest.
//
// Concretely, the canonical hash layout per the design plan is:
//
//	for each sorted relative POSIX path p:
//	    outer.Write([]byte(p))
//	    outer.Write([]byte{0})
//	    outer.Write(<full file contents>)
//	    outer.Write([]byte("\n"))
//	digest = hex(outer.Sum(nil))
//
// The leading path bytes plus NUL separator make the digest sensitive
// to file renames; the trailing LF acts as a record terminator.
//
// Empty-match semantics are GH-faithful: when no pattern matches any
// file, the function returns the empty string and no error. Real
// filesystem errors (read failure mid-glob) propagate so the caller
// sees them — silently swallowing them would let a corrupt repo
// produce a stable but meaningless digest.

func init() { register("hashFiles", hashFilesFn) }

// hashFilesFn implements the funcImpl signature. It is pure with respect
// to its arguments but reads the filesystem via the process cwd; tests
// MUST scope cwd with t.Chdir to keep behaviour deterministic.
func hashFilesFn(args []value) (value, error) {
	if len(args) == 0 {
		return value{}, fmt.Errorf("hashFiles: needs at least one pattern")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return value{}, fmt.Errorf("hashFiles: getwd: %w", err)
	}
	fsys := os.DirFS(cwd)

	matches, err := collectMatches(fsys, args)
	if err != nil {
		return value{}, err
	}
	// Empty match -> empty string, no error (GH-faithful).
	if len(matches) == 0 {
		return stringValue(""), nil
	}
	sort.Strings(matches)

	outer := sha256.New()
	for _, rel := range matches {
		if err := writeFileInto(outer, fsys, rel); err != nil {
			return value{}, err
		}
	}
	return stringValue(hex.EncodeToString(outer.Sum(nil))), nil
}

// collectMatches resolves every pattern in args against fsys and returns
// the deduplicated set of regular-file paths. Splitting this out keeps
// hashFilesFn focused on the hash-build pipeline; directory entries and
// duplicate paths are filtered here so the caller can assume the slice is
// canonical.
func collectMatches(fsys fs.FS, args []value) ([]string, error) {
	seen := map[string]struct{}{}
	var matches []string
	for _, a := range args {
		if a.Kind != kindString {
			return nil, fmt.Errorf("hashFiles: arg must be string, got kind %d", a.Kind)
		}
		pattern, _ := a.Data.(string)
		// doublestar expects forward slashes regardless of host OS, so
		// normalise any user-supplied separators before globbing. The
		// returned paths are already forward-slashed.
		pattern = filepath.ToSlash(pattern)
		m, gerr := doublestar.Glob(fsys, pattern)
		if gerr != nil {
			return nil, fmt.Errorf("hashFiles: glob %q: %w", pattern, gerr)
		}
		if err := appendPatternMatches(fsys, m, seen, &matches); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

// appendPatternMatches walks the raw results from one glob, skips
// directories and already-seen paths, and appends fresh hits to matches.
// Pulled out so collectMatches stays under the gocyclo threshold and so
// the dedupe + stat logic has a single home.
func appendPatternMatches(fsys fs.FS, m []string, seen map[string]struct{}, matches *[]string) error {
	for _, p := range m {
		// doublestar already emits forward-slash paths but ToSlash is
		// a no-op on those — applying it makes the dedupe key
		// canonically POSIX even if a future doublestar release ever
		// changes that contract on Windows.
		posix := filepath.ToSlash(p)
		if _, ok := seen[posix]; ok {
			continue
		}
		info, ierr := fs.Stat(fsys, posix)
		if ierr != nil {
			// A pattern can match a path whose stat then fails (race or
			// permission). Surface this rather than silently dropping it.
			return fmt.Errorf("hashFiles: stat %q: %w", posix, ierr)
		}
		if info.IsDir() {
			// Globs naturally pick up directories when the pattern is
			// loose (e.g. `**/*`). Hashing a directory has no meaning;
			// GH only hashes file contents.
			continue
		}
		seen[posix] = struct{}{}
		*matches = append(*matches, posix)
	}
	return nil
}

// writeFileInto folds one file's contribution into the running outer hash
// using the canonical layout documented above. Split into its own helper
// so the deferred Close pairs cleanly with each Open and so the main loop
// stays readable.
func writeFileInto(outer io.Writer, fsys fs.FS, rel string) error {
	f, err := fsys.Open(rel)
	if err != nil {
		return fmt.Errorf("hashFiles: open %q: %w", rel, err)
	}
	defer f.Close()
	if _, err := outer.Write([]byte(rel)); err != nil {
		return fmt.Errorf("hashFiles: write path for %q: %w", rel, err)
	}
	if _, err := outer.Write([]byte{0}); err != nil {
		return fmt.Errorf("hashFiles: write sep for %q: %w", rel, err)
	}
	if _, err := io.Copy(outer, f); err != nil {
		return fmt.Errorf("hashFiles: read %q: %w", rel, err)
	}
	if _, err := outer.Write([]byte("\n")); err != nil {
		return fmt.Errorf("hashFiles: write terminator for %q: %w", rel, err)
	}
	return nil
}
