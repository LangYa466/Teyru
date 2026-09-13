package mod

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// EnvPath is the environment variable that moves the module cache, Go's
// GOPATH in the small: `$TEYRUPATH/pkg/mod/<module>@<version>`.
const EnvPath = "TEYRUPATH"

// EnvGitURL overrides the base URL a module is fetched from, for a module
// whose repository is not `https://<module path>`: a mirror, a `file://` path
// or a local http server. Go looks this up with a ?go-get=1 request; Teyru
// does not, so a vanity import path names the override instead.
const EnvGitURL = "TEYRU_GIT_URL"

// Cache is the module cache: a directory per module version holding that
// version's sources.
type Cache struct {
	// Root is TEYRUPATH, the cache's own directory; the modules live under it
	// in pkg/mod, so that the layout has room for other build state later.
	Root string
}

// Open returns the cache named by the environment, or the default under the
// user's home directory.
func Open() (*Cache, error) {
	root := os.Getenv(EnvPath)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot find the module cache: %s is not set and the home directory is unknown: %w", EnvPath, err)
		}
		root = filepath.Join(home, ".teyru")
	}
	return &Cache{Root: root}, nil
}

// ModRoot is the directory the extracted modules live in.
func (c *Cache) ModRoot() string { return filepath.Join(c.Root, "pkg", "mod") }

// Dir is where a module version's sources are, whether or not they are there.
func (c *Cache) Dir(module, version string) string {
	return filepath.Join(c.ModRoot(), filepath.FromSlash(module)+"@"+version)
}

// Has reports whether a module version is already extracted.
func (c *Cache) Has(module, version string) bool {
	st, err := os.Stat(c.Dir(module, version))
	return err == nil && st.IsDir()
}

// Installed is one module version found in the cache.
type Installed struct {
	Module  string
	Version Version
	Text    string // the version as written
	Dir     string
}

// List walks the cache and reports every module version in it. Only the
// directory names are read: this is how `teyru mod tidy` learns which versions
// the disk can satisfy without any network access.
func (c *Cache) List() ([]Installed, error) {
	root := c.ModRoot()
	var out []Installed
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// a version directory is the first element carrying an @; below it the
		// path is the module's own package tree, which this walk does not read
		i := strings.LastIndexByte(rel, '@')
		if i < 0 {
			return nil
		}
		module, version := rel[:i], rel[i+1:]
		if !ValidPath(module) {
			return filepath.SkipDir
		}
		v, err := ParseVersion(version)
		if err != nil {
			return filepath.SkipDir
		}
		out = append(out, Installed{Module: module, Version: v, Text: version, Dir: p})
		return filepath.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the module cache %s: %w", root, err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return Compare(out[i].Version, out[j].Version) < 0
	})
	return out, nil
}

// Fetcher puts the sources of one module version into dst, which is empty and
// belongs to the caller. It is an interface because the fetch is the one part
// of the module system that talks to the network, and every test of the rest
// of it has to run without one.
type Fetcher interface {
	Fetch(module, version, dst string) error
}

// GitFetcher fetches a module with git: a shallow clone of the tag that has
// the module's version as its name. There is no proxy and no zip protocol --
// the module path is the repository URL, so what is fetched is what the
// author pushed, and the tag is the version.
type GitFetcher struct {
	// Remote is the base URL modules are fetched from. Empty means https://.
	Remote string
	// Log receives a line per command run, for -v; it may be nil.
	Log func(format string, args ...any)
}

// NewGitFetcher returns the fetcher the command line uses: $TEYRU_GIT_URL when
// it is set, https:// otherwise.
func NewGitFetcher() *GitFetcher {
	return &GitFetcher{Remote: os.Getenv(EnvGitURL)}
}

func (f *GitFetcher) url(module string) string {
	remote := f.Remote
	if remote == "" {
		remote = "https://"
	}
	return strings.TrimSuffix(remote, "/") + "/" + module
}

// Fetch clones the module's repository at the tag named by version.
func (f *GitFetcher) Fetch(module, version, dst string) error {
	if err := CheckImportPath(module, version); err != nil {
		return err
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("cannot fetch %s@%s: git is not on PATH", module, version)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	url := f.url(module)
	// --depth 1 with an explicit tag: a module version is one commit, and a
	// full clone of a large repository to read one tag is time the build does
	// not have.
	args := []string{"clone", "--depth", "1", "--branch", version, "--quiet", url, dst}
	if f.Log != nil {
		f.Log("%s %s", git, strings.Join(args, " "))
	}
	cmd := exec.Command(git, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cannot fetch %s@%s from %s: %w", module, version, url, err)
	}
	// The clone's history is noise: a module is hashed by its sources, and a
	// .git directory inside the cache would double its size for nothing.
	return os.RemoveAll(filepath.Join(dst, ".git"))
}

// LocalFetcher copies a module out of a directory, which is what makes the
// fetch path testable with no network and what a mirror of pre-fetched modules
// looks like. It reads the layout the cache itself uses:
//
//	<root>/<module>@<version>/
type LocalFetcher struct {
	Root string
	// Log receives a line per copy, for -v; it may be nil.
	Log func(format string, args ...any)
}

// Fetch copies the module's directory into dst.
func (f *LocalFetcher) Fetch(module, version, dst string) error {
	src := filepath.Join(f.Root, filepath.FromSlash(module)+"@"+version)
	st, err := os.Stat(src)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("cannot fetch %s@%s: %s is not a directory", module, version, src)
	}
	if f.Log != nil {
		f.Log("copy %s -> %s", src, dst)
	}
	return copyTree(src, dst)
}

// Fetch installs a module version into the cache, returning the directory it
// now occupies. A version that is already cached is not fetched again: a
// module version is immutable, so a second copy of it can only differ by being
// wrong, and the checksum re-checked at every build is what proves it.
func (c *Cache) Fetch(f Fetcher, module, version string) (string, error) {
	dst := c.Dir(module, version)
	if c.Has(module, version) {
		return dst, nil
	}
	if err := CheckImportPath(module, version); err != nil {
		return "", err
	}
	// The fetch happens next to its final place so that installing it is a
	// rename: a cache entry appears complete or not at all, and a build
	// reading the cache never sees a half-written module.
	tmp, err := os.MkdirTemp(filepath.Join(c.Root, "pkg", "mod", "cache"), "fetch-")
	if err != nil {
		// the cache directory itself may not exist yet
		if err := os.MkdirAll(filepath.Join(c.Root, "pkg", "mod", "cache"), 0o755); err != nil {
			return "", err
		}
		if tmp, err = os.MkdirTemp(filepath.Join(c.Root, "pkg", "mod", "cache"), "fetch-"); err != nil {
			return "", err
		}
	}
	defer os.RemoveAll(tmp)
	stage := filepath.Join(tmp, "src")
	if err := f.Fetch(module, version, stage); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	// A failed rename (a leftover directory, another filesystem) falls back to
	// a copy, so the cache still ends up with the module in it.
	if err := os.Rename(stage, dst); err != nil {
		if err := copyTree(stage, dst); err != nil {
			return "", fmt.Errorf("cannot install %s@%s into the cache: %w", module, version, err)
		}
	}
	return dst, nil
}

// copyTree copies the contents of src, which must be a directory, to dst.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// PackageDir joins a module version's directory with the package's path
// inside it, which is the module-relative directory spelled with slashes.
func (c *Cache) PackageDir(module, version, rel string) string {
	dir := c.Dir(module, version)
	if rel == "" || rel == "." {
		return dir
	}
	return filepath.Join(dir, filepath.FromSlash(path.Clean(rel)))
}
