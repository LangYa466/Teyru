package mod

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// SumFileName is the name of the checksum file that sits next to teyru.mod.
const SumFileName = "teyru.sum"

// Sum is a parsed teyru.sum: one line per hashed file tree, of the form
//
//	example.com/dep v1.2.3 h1:...
//	example.com/dep v1.2.3/teyru.mod h1:...
//
// The second form covers the dependency's own teyru.mod, whose bytes decide
// which modules the build list contains: without it, editing one line of a
// cached teyru.mod would redirect the build without changing any source.
type Sum struct {
	lines []sumLine
}

type sumLine struct {
	Mod     string
	Ver     string
	ModFile bool
	Hash    string
}

// ParseSum parses a checksum file. name is used in error messages only.
func ParseSum(name string, data []byte) (*Sum, error) {
	s := &Sum{}
	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		text, _ := splitComment(line)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 3 {
			return nil, &Error{File: name, Line: i + 1, Msg: "want <module> <version> h1:<hash>"}
		}
		l := sumLine{Mod: fields[0], Ver: fields[1], Hash: fields[2]}
		if mod, ok := strings.CutSuffix(l.Ver, "/"+ModuleFileName); ok {
			l.Ver, l.ModFile = mod, true
		}
		if !ValidPath(l.Mod) {
			return nil, &Error{File: name, Line: i + 1, Msg: fmt.Sprintf("%q is not a module path", l.Mod)}
		}
		if _, err := ParseVersion(l.Ver); err != nil {
			return nil, &Error{File: name, Line: i + 1, Msg: err.Error()}
		}
		if !strings.HasPrefix(l.Hash, "h1:") {
			return nil, &Error{File: name, Line: i + 1, Msg: fmt.Sprintf("%q is not an h1: hash", l.Hash)}
		}
		s.lines = append(s.lines, l)
	}
	return s, nil
}

// LoadSum reads a checksum file. A missing file is an empty sum rather than an
// error: a module that lists no requirements has nothing to checksum, and the
// caller decides what a missing line means.
func LoadSum(path string) (*Sum, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Sum{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseSum(path, data)
}

// Format renders the file: one line per entry, sorted, each with a trailing
// newline. Sorting makes the file stable, so two builds that add the same
// requirements in a different order write the same bytes and `git diff` shows
// only real changes.
func (s *Sum) Format() []byte {
	lines := append([]sumLine(nil), s.lines...)
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := lines[i], lines[j]
		if a.Mod != b.Mod {
			return a.Mod < b.Mod
		}
		if a.Ver != b.Ver {
			return a.Ver < b.Ver
		}
		// the module file's line follows the tree's, so that a diff of a
		// changed module reads as one entry
		return !a.ModFile && b.ModFile
	})
	var b strings.Builder
	for _, l := range lines {
		ver := l.Ver
		if l.ModFile {
			ver += "/" + ModuleFileName
		}
		fmt.Fprintf(&b, "%s %s %s\n", l.Mod, ver, l.Hash)
	}
	return []byte(b.String())
}

// Write writes the canonical form to path.
func (s *Sum) Write(path string) error {
	if err := os.WriteFile(path, s.Format(), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// Lookup returns the recorded hash for a module version, and whether there is
// one at all. A module with no recorded hash is not the same as one whose
// hash differs: the first is a module nobody fetched, the second is a module
// whose contents changed under the same version.
func (s *Sum) Lookup(mod, ver string, modFile bool) (string, bool) {
	for _, l := range s.lines {
		if l.Mod == mod && l.Ver == ver && l.ModFile == modFile {
			return l.Hash, true
		}
	}
	return "", false
}

// Set records a hash, replacing any earlier one.
func (s *Sum) Set(mod, ver, hash string, modFile bool) {
	for i, l := range s.lines {
		if l.Mod == mod && l.Ver == ver && l.ModFile == modFile {
			s.lines[i].Hash = hash
			return
		}
	}
	s.lines = append(s.lines, sumLine{Mod: mod, Ver: ver, ModFile: modFile, Hash: hash})
}

// DropModule removes every entry of a module version.
func (s *Sum) DropModule(mod, ver string) {
	out := s.lines[:0]
	for _, l := range s.lines {
		if l.Mod == mod && l.Ver == ver {
			continue
		}
		out = append(out, l)
	}
	s.lines = out
}

// Modules lists the module versions the file covers, in sorted order.
func (s *Sum) Modules() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, l := range s.lines {
		key := l.Mod + "@" + l.Ver
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// HashDir hashes a module tree the way Go hashes a module zip: the hash of the
// sorted list of `sha256(file)  name` lines, base64ed and prefixed with h1:.
// Names are relative to dir and always slashed, so the same tree hashes the
// same on any platform.
//
// Directories whose name starts with a dot are skipped. A fetched module has
// its .git directory inside it until it is installed, and a VCS directory is
// not part of the module: hashing it would make the checksum depend on the
// clone, not on the sources.
func HashDir(dir string) (string, error) {
	root := os.DirFS(dir)
	var names []string
	err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name != "." && strings.HasPrefix(path.Base(name), ".") {
				return fs.SkipDir
			}
			return nil
		}
		// A symlink is not a file whose bytes are part of the module, and
		// following one would let a module include anything on the disk.
		if !d.Type().IsRegular() {
			return nil
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fh, err := hashOpen(root, name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%x  %s\n", fh, name)
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// HashFile hashes one file's contents.
func HashFile(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func hashOpen(root fs.FS, name string) ([]byte, error) {
	fh, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// TreeHash hashes a fetched module the way the build expects to find it: the
// tree of its sources plus, when the module ships one, its own teyru.mod. The
// two hashes are returned separately because they are recorded separately.
func TreeHash(dir string) (tree, modFile string, err error) {
	tree, err = HashDir(dir)
	if err != nil {
		return "", "", err
	}
	modPath := filepath.Join(dir, ModuleFileName)
	if _, err := os.Stat(modPath); err == nil {
		modFile, err = HashFile(modPath)
		if err != nil {
			return "", "", err
		}
	}
	return tree, modFile, nil
}
