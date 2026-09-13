// Package mod implements Teyru's module system: the teyru.mod file, the
// teyru.sum checksum file, the module cache and the import resolution that
// maps an import path onto the directory holding its sources.
//
// The design follows Go's module system, and so do the names: a module is a
// tree of packages versioned and fetched as a unit, an import path is the
// module path plus the package's directory, and a module's identity is that
// path -- not a bare package name, which two modules may share.
package mod

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a module version, and therefore the git tag it was fetched from:
// vMAJOR.MINOR.PATCH with an optional prerelease suffix (`v0.1.0`, `v1.2.3`,
// `v2.0.0-rc1`). Build metadata (`+incompatible`) is not accepted: it names no
// distinct tag, so it can only ever be a second spelling of a version that is
// already there.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseVersion parses a canonical version string.
func ParseVersion(s string) (Version, error) {
	v := Version{}
	bad := fmt.Errorf("invalid module version %q: want vMAJOR.MINOR.PATCH", s)
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return v, bad
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		v.Pre, rest = rest[i+1:], rest[:i]
		if v.Pre == "" {
			return v, bad
		}
	}
	fields := strings.Split(rest, ".")
	if len(fields) != 3 {
		return v, bad
	}
	for i, dst := range []*int{&v.Major, &v.Minor, &v.Patch} {
		// Leading zeros are rejected: `v01.2.3` and `v1.2.3` would be two
		// spellings of one tag, and a version string is a cache directory name.
		if fields[i] == "" || len(fields[i]) > 1 && fields[i][0] == '0' {
			return v, bad
		}
		n, err := strconv.Atoi(fields[i])
		if err != nil || n < 0 {
			return v, bad
		}
		*dst = n
	}
	return v, nil
}

// String returns the canonical spelling of the version.
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders two versions: -1, 0 or 1. A prerelease sorts before the
// release it belongs to, and prerelease identifiers are compared the way
// semver does (numeric identifiers numerically, so v1.0.0-rc2 < v1.0.0-rc10,
// which a byte comparison gets wrong).
func Compare(a, b Version) int {
	for _, d := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if d[0] != d[1] {
			return sign(d[0] - d[1])
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	return comparePre(a.Pre, b.Pre)
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aerr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case berr == nil:
			return 1
		default:
			if as[i] != bs[i] {
				return sign(strings.Compare(as[i], bs[i]))
			}
		}
	}
	return sign(len(as) - len(bs))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// CheckImportPath enforces semantic import versioning: from v2 on, the major
// version is part of the import path (`example.com/m/v2`), because v1 and v2
// of a module are different modules that a program may use at the same time.
// Without the suffix, a v2 tag fetched under its v1 path would silently
// replace the v1 sources in every program that imports them.
func CheckImportPath(modulePath, version string) error {
	// Path validity is checked here rather than at each of the callers because
	// this is the one check a fetch, a cache lookup and the command line all
	// make, and a module path is also a directory name in the cache.
	if !ValidPath(modulePath) {
		return fmt.Errorf("%q is not a module path", modulePath)
	}
	v, err := ParseVersion(version)
	if err != nil {
		return err
	}
	// The two have to agree in both directions: a v1 tag fetched under the v2
	// path would hand a program that asked for v2 a copy of the v1 API, and a
	// v2 tag under the v1 path would silently replace every v1 program's copy.
	if n, ok := majorSuffix(modulePath); ok {
		if n != v.Major {
			return fmt.Errorf("module %s cannot be %s: the path says v%d and the version says v%d",
				modulePath, version, n, v.Major)
		}
		return nil
	}
	if v.Major < 2 {
		return nil
	}
	return fmt.Errorf("module %s cannot be %s: from v2 on the major version belongs to the import path, so the module is %s/v%d",
		modulePath, version, modulePath, v.Major)
}

// majorSuffix returns the N of a `/vN` last element, the part of a module path
// that names the major version.
func majorSuffix(modulePath string) (int, bool) {
	i := strings.LastIndexByte(modulePath, '/')
	if i < 0 {
		return 0, false
	}
	last := modulePath[i+1:]
	if len(last) < 2 || last[0] != 'v' {
		return 0, false
	}
	n, err := strconv.Atoi(last[1:])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// ValidPath reports whether s can be a module path or an import path: one or
// more non-empty elements of letters, digits and a few separators. The rules
// are Go's because the path is also what an import statement has to spell.
func ValidPath(s string) bool {
	if s == "" || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return false
	}
	for _, elem := range strings.Split(s, "/") {
		// `.` and `..` are directories, not names. A module path is also the
		// path of a directory in the cache, so one holding either would name a
		// directory of the user's own -- `teyru get ../../x@v1.0.0` would fetch
		// outside the cache it was told to fetch into.
		if elem == "" || elem == "." || elem == ".." {
			return false
		}
		for i := 0; i < len(elem); i++ {
			c := elem[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			case c == '.' || c == '_' || c == '-' || c == '~':
			default:
				return false
			}
		}
	}
	return true
}

// DottedPath spells a module path the way an import statement writes it:
// `example.com/m/pkg` becomes `example.com.m.pkg`. Teyru's import syntax is
// Java's, whose path elements are separated by dots and which has no `/`, so
// the slash spelling is folded onto dots and the driver folds it back.
func DottedPath(path string) string {
	return strings.ReplaceAll(path, "/", ".")
}
