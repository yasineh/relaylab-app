package doccheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..", "..")
}

// docFiles are README.md and every Markdown file under docs/.
func docFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	files := []string{filepath.Join(root, "README.md")}
	_ = filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return nil
	})
	return files
}

var (
	linkRe    = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	headingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*#*\s*$`)
	fenceRe   = regexp.MustCompile("(?s)```.*?```")
	codeRe    = regexp.MustCompile("`[^`\n]*`")
	slugDrop  = regexp.MustCompile(`[^\p{L}\p{N} _-]`)
)

// slug is GitHub's heading anchor: lower case, punctuation removed, spaces
// to hyphens. Duplicates get -1, -2, ...
func slug(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`).ReplaceAllString(h, "$1") // [text](url) -> text
	h = slugDrop.ReplaceAllString(h, "")
	return strings.ReplaceAll(h, " ", "-")
}

func anchors(t *testing.T, path string, cache map[string]map[string]bool) map[string]bool {
	if a, ok := cache[path]; ok {
		return a
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a := map[string]bool{}
	seen := map[string]int{}
	for _, m := range headingRe.FindAllStringSubmatch(fenceRe.ReplaceAllString(string(b), ""), -1) {
		s := slug(m[1])
		if n := seen[s]; n > 0 {
			a[s+"-"+strconv.Itoa(n)] = true
		} else {
			a[s] = true
		}
		seen[s]++
	}
	cache[path] = a
	return a
}

// Every relative link in README.md and docs/ points at an existing file and,
// when it has one, an existing heading anchor. External links are not
// fetched (they would make the check depend on the network).
func TestRelativeLinksResolve(t *testing.T) {
	root := repoRoot(t)
	cache := map[string]map[string]bool{}
	checked := 0
	for _, f := range docFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := codeRe.ReplaceAllString(fenceRe.ReplaceAllString(string(b), ""), "")
		rel, _ := filepath.Rel(root, f)
		for _, m := range linkRe.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			checked++
			path, frag, _ := strings.Cut(target, "#")
			dest := f
			if path != "" {
				dest = filepath.Join(filepath.Dir(f), path)
				info, err := os.Stat(dest)
				if err != nil {
					t.Errorf("%s: %s does not exist", rel, target)
					continue
				}
				if info.IsDir() {
					if frag != "" {
						t.Errorf("%s: %s anchors into a directory", rel, target)
					}
					continue
				}
			}
			if frag != "" && strings.HasSuffix(dest, ".md") && !anchors(t, dest, cache)[frag] {
				t.Errorf("%s: %s has no heading #%s", rel, target, frag)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d relative links checked", checked)
	}
}
