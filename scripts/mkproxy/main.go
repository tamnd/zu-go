// Command mkproxy writes a module proxy for the modules in this
// repository, so that an install can be tested before there is a
// release to install.
//
//	go run ./scripts/mkproxy -out /tmp/proxy
//
// What it makes is the layout a GOPROXY is read off a filesystem: for
// every module here, a .info, a .mod and a .zip under <module>/@v/.
// Point GOPROXY at it with a file:// URL and `go get
// github.com/tamnd/zu-go` resolves through the same requirements, the
// same nested modules and the same archive it will resolve through off
// the public proxy once there is a tag.
//
// Version v0.0.0 for all of them, which is no version anybody releases
// and is exactly what every go.mod here already requires, so nothing
// has to be rewritten to make this work. That is the point rather than
// a convenience: a test that edited the requirements first would be
// testing requirements nobody ships.
//
// The files in each zip are the files git is tracking, and not what a
// walk of the directory finds. A tag publishes the tree, so an editor's
// backup file or a stale archive left in a working copy belongs in
// neither, and the difference between the two is the difference between
// a test that says the same thing everywhere and one that says
// something else on the machine it was written on.
//
// This is not a substitute for installing a release. It leaves out the
// network, the checksum database and the proxy's own reading of a tag,
// which are the three things that cannot exist before the tag does.
// What it covers is everything inside this repository: that the module
// graph closes with no workspace holding it up, that the static library
// is inside the zip a user downloads, and that what comes out builds
// and runs on a machine with nothing but Go on it.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("mkproxy: ")

	out := flag.String("out", "", "the directory to write the proxy into")
	root := flag.String("root", ".", "the repository to read the modules from")
	version := flag.String("version", "v0.0.0", "the version to publish every module at")
	flag.Parse()

	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*root, *out, *version); err != nil {
		log.Fatal(err)
	}
}

func run(root, out, version string) error {
	mods, err := modules(root)
	if err != nil {
		return err
	}
	if len(mods) == 0 {
		return fmt.Errorf("no module under %s, which is not a repository this belongs in", root)
	}
	for _, m := range mods {
		if err := publish(m, mods, out, version); err != nil {
			return fmt.Errorf("%s: %w", m.path, err)
		}
		fmt.Printf("%s@%s\n", m.path, version)
	}
	return nil
}

// A module is one go.mod in this repository: where it is and what it
// calls itself.
type module struct {
	// dir is relative to the repository root, with "." for the root
	// module, because that is the shape both the walk and the zip
	// prefixes want.
	dir  string
	path string
}

// modules finds every module in the repository, in the order a walk
// meets them, which puts a parent before the children nested inside it.
func modules(root string) ([]module, error) {
	var found []module
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The directories a version control system keeps for
			// itself, which are the ones a publish leaves out.
			switch d.Name() {
			case ".git", ".hg", ".svn", ".bzr":
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		name, err := modulePath(b)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		dir, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		found = append(found, module{dir: filepath.ToSlash(dir), path: name})
		return nil
	})
	return found, err
}

// modulePath reads the module line out of a go.mod. The file has
// comments above it here, so the first line is not the one wanted.
func modulePath(b []byte) (string, error) {
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "module ")
		if !ok {
			continue
		}
		p := strings.TrimSpace(rest)
		if p == "" {
			break
		}
		return p, nil
	}
	return "", fmt.Errorf("no module line")
}

// publish writes the three files the proxy protocol answers with for
// one version of one module.
func publish(m module, all []module, out, version string) error {
	dir := filepath.Join(out, filepath.FromSlash(escape(m.path)), "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// The versions on offer, which is the file the resolver reads when
	// it is asked for a package rather than for a version. An import
	// with nothing requiring it yet is the ordinary way somebody
	// installs this, so leaving it out would leave out the path most
	// people take.
	if err := os.WriteFile(filepath.Join(dir, "list"), []byte(version+"\n"), 0o644); err != nil {
		return err
	}

	// A fixed time rather than the commit's, so that two runs of this
	// on the same tree write the same bytes.
	info := fmt.Sprintf("{%q:%q,%q:%q}\n", "Version", version, "Time", "2000-01-01T00:00:00Z")
	if err := os.WriteFile(filepath.Join(dir, version+".info"), []byte(info), 0o644); err != nil {
		return err
	}

	gomod, err := os.ReadFile(filepath.Join(m.dir, "go.mod"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, version+".mod"), gomod, 0o644); err != nil {
		return err
	}

	z, err := archive(m, all, version)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, version+".zip"), z, 0o644)
}

// archive builds the module zip, which is every file the module owns
// under one <module>@<version>/ prefix. A module owns the files git is
// tracking in its directory and does not own anything in a directory
// with a go.mod of its own, so the client's zip carries neither the
// libraries nor the Arrow reader and each of those is downloaded only
// by whoever imports it. That is the whole reason they are modules.
func archive(m module, all []module, version string) ([]byte, error) {
	names, err := tracked(m.dir)
	if err != nil {
		return nil, err
	}

	var nested []string
	for _, o := range all {
		if o.dir == m.dir {
			continue
		}
		rel, err := filepath.Rel(m.dir, o.dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		nested = append(nested, filepath.ToSlash(rel)+"/")
	}

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	prefix := m.path + "@" + version + "/"
	for _, name := range names {
		if slices.ContainsFunc(nested, func(p string) bool { return strings.HasPrefix(name, p) }) {
			continue
		}
		// A tracked path that is not a regular file is a symlink or a
		// submodule, and a module zip holds neither.
		src := filepath.Join(m.dir, filepath.FromSlash(name))
		st, err := os.Lstat(src)
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		e, err := w.Create(prefix + path.Clean(name))
		if err != nil {
			f.Close()
			return nil, err
		}
		_, err = io.Copy(e, f)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// tracked is the file list a tag would publish, asked of git rather
// than of the filesystem.
func tracked(dir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var names []string
	for _, name := range strings.Split(string(b), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// escape is the proxy's own spelling of a module path, where an upper
// case letter becomes an exclamation mark and the lower case of it, so
// that two paths differing only in case cannot be one directory on a
// filesystem that does not tell them apart.
func escape(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
