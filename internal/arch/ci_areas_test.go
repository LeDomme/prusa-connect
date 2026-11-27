package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// scripts/ci-areas.sh tells `CI / What the change touches` which areas a pull
// request touches, and a job in pathGatedJobs skips when none of its areas
// did. A file the script puts in the wrong area is a skip nobody sees, so the
// tests here hold the script three ways:
//
//   - it sorts a set of known paths the way ADR-20260923-xcq rule 1 says
//   - the areas it prints are the outputs of the paths job
//   - every file outside its own area that Go code, the web packages or the
//     documentation build reads is in the area of the jobs that read it
//
// The third is the one that finds a coupling nobody wrote down. A Go test that
// starts to read a file under docs/ fails here until the script puts that
// file in the `go` area too.

// ciAreasScript is the script the paths job runs.
const ciAreasScript = "scripts/ci-areas.sh"

// ciAreas is every area the script prints, in its order.
//
//nolint:gochecknoglobals // a closed list, read by the tests below.
var ciAreas = []string{"all", "go", "web", "docs", "changes", "removed", "sign_in"}

// runCIAreas runs the script over lines of `git diff --name-status` and
// returns the areas it called true.
func runCIAreas(t *testing.T, bash, root, input string, args ...string) map[string]bool {
	t.Helper()

	cmd := exec.Command(bash, append([]string{filepath.Join(root, ciAreasScript)}, args...)...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(input)

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", ciAreasScript, args, err, stderr.String())
	}

	areas := map[string]bool{}
	seen := 0

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok || (value != "true" && value != "false") {
			t.Fatalf("%s printed %q, which is not name=true or name=false", ciAreasScript, line)
		}

		seen++

		if value == "true" {
			areas[name] = true
		}
	}

	if seen != len(ciAreas) {
		t.Fatalf("%s printed %d areas, want %d: %s", ciAreasScript, seen, len(ciAreas), out)
	}

	return areas
}

// lookUpBash finds bash, or skips the test.
func lookUpBash(t *testing.T) string {
	t.Helper()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash on PATH, so %s cannot be run here: %v", ciAreasScript, err)
	}

	return bash
}

// TestTheAreaScriptSortsEachPath runs the script over paths whose areas
// ADR-20260923-xcq rule 1 fixes.
func TestTheAreaScriptSortsEachPath(t *testing.T) {
	t.Parallel()

	bash := lookUpBash(t)
	root := repoRoot(t)

	every := slices.Clone(ciAreas)

	for name, testCase := range map[string]struct {
		input string
		want  []string
	}{
		"a changelog fragment":                  {"A\tdocs/changes/a-fix.md\n", []string{"changes"}},
		"an ADR":                                {"M\tdocs/decisions/0001-x.md\n", []string{"docs"}},
		"a page of the site":                    {"M\tdocs/src/content/docs/mqtt.md\n", []string{"docs"}},
		"a page a generator writes":             {"M\tdocs/src/content/docs/error-codes.md\n", []string{"go", "docs"}},
		"the error code registry":               {"M\tdocs/errors/error-codes.json\n", []string{"go", "web", "docs"}},
		"a Go file":                             {"M\tinternal/app/core.go\n", []string{"go"}},
		"the sdk":                               {"M\tsdk/eventsapi/envelope.go\n", []string{"go"}},
		"a Go module under packages/":           {"M\tpackages/fractal/fractal.go\n", []string{"go"}},
		"a README in the Go tree":               {"M\tsdk/README.md\n", []string{"go", "docs"}},
		"a file of the web app":                 {"M\tfrontend/src/lib/x.svelte\n", []string{"web"}},
		"a component":                           {"M\tpackages/svelte/src/primitives/button/button.svelte\n", []string{"web"}},
		"the design CLI":                        {"M\ttools/design/src/cli.ts\n", []string{"web"}},
		"the design deprecations":               {"M\tdesign/deprecations.json\n", []string{"web"}},
		"the generated client":                  {"M\tpackages/typescript/src/generated/core-api.ts\n", []string{"go", "web"}},
		"a file of the library a Go test reads": {"M\tpackages/svelte/src/styles/tokens.css\n", []string{"go", "web"}},
		"an example module":                     {"M\texamples/spools/main.go\n", []string{"go", "web"}},
		"the sign-in screen":                    {"M\tpackages/svelte/src/shell/sign-in/x.svelte\n", []string{"web", "sign_in"}},
		"the login code":                        {"M\tinternal/platform/auth/x.go\n", []string{"go", "sign_in"}},
		"the sign-in lane's script":             {"M\tscripts/check-oidc-providers.sh\n", []string{"sign_in"}},
		"another workflow":                      {"M\t.github/workflows/nightly.yml\n", nil},
		"the agents' rules":                     {"M\t.agents/rules/git-discipline.md\n", []string{"docs"}},
		"a deleted ADR":                         {"D\tdocs/decisions/0001-x.md\n", []string{"docs", "removed"}},
		"a renamed Go file":                     {"R100\tinternal/a.go\tinternal/b.go\n", []string{"go", "removed"}},
		"a path with no status":                 {"frontend/src/app.css\n", []string{"web"}},
		"no change at all":                      {"", nil},
		"ci.yml":                                {"M\t.github/workflows/ci.yml\n", every},
		"the area script":                       {"M\t" + ciAreasScript + "\n", every},
		"the Makefile":                          {"M\tMakefile\n", every},
		"a shared script":                       {"M\tscripts/check-writing-style.sh\n", every},
		"go.sum":                                {"M\tgo.sum\n", every},
		"a lockfile":                            {"M\tfrontend/pnpm-lock.yaml\n", every},
		"a manifest":                            {"M\tpackages/svelte/package.json\n", every},
		"the Node.js pin":                       {"M\t.nvmrc\n", every},
		"VERSION":                               {"M\tVERSION\n", every},
		"a fixture":                             {"M\tdocker/postgres-fixture/Dockerfile\n", every},
		"the image's Dockerfile":                {"M\tdocker/server/Dockerfile\n", every},
		"a path no rule names":                  {"M\tLICENSE\n", every},
		"a top-level folder no rule names":      {"A\tnew-folder/x.txt\n", every},
		"a path git quotes":                     {"M\t\"docs/caf\\303\\251.md\"\n", every},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := runCIAreas(t, bash, root, testCase.input)

			want := map[string]bool{}
			for _, area := range testCase.want {
				want[area] = true
			}

			for _, area := range ciAreas {
				if got[area] != want[area] {
					t.Errorf("%s: %s=%t, want %t", strings.TrimSpace(testCase.input), area, got[area], want[area])
				}
			}
		})
	}

	t.Run("--all", func(t *testing.T) {
		t.Parallel()

		got := runCIAreas(t, bash, root, "", "--all")
		for _, area := range ciAreas {
			if !got[area] {
				t.Errorf("--all printed %s=false", area)
			}
		}
	})
}

// TestTheAreasAreTheOutputsOfThePathsJob holds the script, the paths job's
// outputs and the areas pathGatedJobs reads to one list. An output the
// script does not print is empty, and an empty answer runs the job, so the
// drift is safe. It is still a name that means nothing.
func TestTheAreasAreTheOutputsOfThePathsJob(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), ciWorkflow))
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflow, err)
	}

	var workflow ciWorkflowJobs
	if err = yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("parsing %s: %v", ciWorkflow, err)
	}

	outputs := workflow.Jobs[changesJob].Outputs

	var names []string

	for name, value := range outputs {
		names = append(names, name)

		if want := "${{ steps.areas.outputs." + name + " }}"; value != want {
			t.Errorf("output %s of job %q is %q, want %q", name, changesJob, value, want)
		}
	}

	sort.Strings(names)

	want := slices.Clone(ciAreas)
	sort.Strings(want)

	if !slices.Equal(names, want) {
		t.Errorf("job %q gives the outputs %v, and %s prints %v", changesJob, names, ciAreasScript, want)
	}

	for id, job := range pathGatedJobs {
		for _, area := range job.areas {
			if !slices.Contains(ciAreas, area) {
				t.Errorf("pathGatedJobs gates %q on %q, which %s does not print", id, area, ciAreasScript)
			}
		}
	}
}

// areaReadExempt names a file a reader names that is not a read of this
// checkout, or whose read another gate covers, keyed by the reader and the
// path, against the reason. It is checked both ways: an entry nothing names
// any more fails.
//
//nolint:gochecknoglobals // a closed list, read by one test.
var areaReadExempt = map[string]string{
	"cmd/generate-config-reference/main_test.go docs/src/content/docs": "It asks whether the page each " +
		"configuration key's See names exists. `make config-reference-check` runs the same checkSee, and it " +
		"runs in `Backend / Verify generated code` and `Documentation (Starlight)`, which both run on a " +
		"change to docs/.",
	"cmd/new-module/main_test.go packages/svelte": "It makes the folder in a scratch checkout it builds " +
		"in a temporary directory, and reads nothing of this one.",
}

// areaReader is one place code that reads files lives, with the areas one of
// which a file it reads must be in.
type areaReader struct {
	what string
	// roots are the folders the reader's code is in.
	roots []string
	// skip are folders under a root that are another reader, or not code.
	skip []string
	// accepts are the areas whose jobs run this code.
	accepts []string
	// floor is the fewest files the walk must read.
	floor int
	// names reads the paths one file names. base is the folder of the
	// reader's root, relative to the repository.
	names func(file, base string, body []byte) []string
}

// skippedDirs are never code a CI job runs from this tree.
//
//nolint:gochecknoglobals // a closed list, read by one test.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "storybook-static": true, ".svelte-kit": true,
	"test-results": true, "coverage": true, ".astro": true, "out": true, "appdist": true,
}

// TestEveryFileACIJobReadsIsInItsArea reads the paths Go code, the web
// packages and the documentation build name. Each path that is a file or a
// folder of this checkout must be in an area whose jobs run that code. A
// path to a folder stands for every file under it.
func TestEveryFileACIJobReadsIsInItsArea(t *testing.T) {
	t.Parallel()

	bash := lookUpBash(t)
	root := repoRoot(t)

	readers := []areaReader{
		{
			what: "Go code",
			roots: []string{
				"cmd", "internal", "adapters", "sdk", "test", "tools", "config", "compose", "deploy",
				"examples", "packages/fractal", "packages/graphql",
			},
			skip:    []string{"internal/arch"},
			accepts: []string{"go"},
			floor:   500,
			names:   goPathNames,
		},
		{
			what:    "code in the web packages",
			roots:   []string{"frontend", "packages/svelte", "packages/typescript", "packages/standards"},
			accepts: []string{"web"},
			floor:   100,
			names:   relativePathNames,
		},
		{
			what:    "the documentation build",
			roots:   []string{"docs"},
			skip:    []string{"docs/src/content", "docs/changes", "docs/decisions"},
			accepts: []string{"go", "docs"},
			floor:   3,
			names:   relativePathNames,
		},
	}

	used := map[string]bool{}
	checked := 0
	checkout := checkoutPaths(t, root)

	for _, reader := range readers {
		paths, files := readerPaths(t, root, reader)

		requireScanned(t, "files of "+reader.what, files, reader.floor)

		var probes []string

		probeOf := map[string]string{}

		for target := range paths {
			isDir, inCheckout := checkout[target]
			if !inCheckout {
				continue
			}

			probe := target
			if isDir {
				probe = target + "/a-file-under-it"
			}

			probes = append(probes, probe)
			probeOf[probe] = target
		}

		sort.Strings(probes)

		for _, probe := range probes {
			target := probeOf[probe]
			got := runCIAreas(t, bash, root, "M\t"+probe+"\n")
			checked++

			if got["all"] || slices.ContainsFunc(reader.accepts, func(area string) bool { return got[area] }) {
				continue
			}

			var unexplained []string

			for _, reading := range paths[target] {
				key := reading + " " + target
				if _, exempt := areaReadExempt[key]; exempt {
					used[key] = true

					continue
				}

				unexplained = append(unexplained, reading)
			}

			if len(unexplained) > 0 {
				t.Errorf("%s reads %s, and %s does not put it in %s.\n"+
					"    Read by: %s\n"+
					"    A change to it would skip the jobs that run that code. Put it in the area in %s,\n"+
					"    or name the reader in areaReadExempt with the reason it is not a read of this checkout.",
					reader.what, target, ciAreasScript, strings.Join(reader.accepts, " or "),
					strings.Join(unexplained, ", "), ciAreasScript)
			}
		}
	}

	requireScanned(t, "paths checked against the areas", checked, 30)

	for key := range areaReadExempt {
		if !used[key] {
			t.Errorf("areaReadExempt names %q, and no reader names that path outside its area any more. "+
				"Delete the entry.", key)
		}
	}
}

// checkoutPaths is every file git tracks or would add, and every folder above
// one, against whether it is a folder. It is the tree a CI job checks out: a
// build output or a note git ignores exists on one machine only.
func checkoutPaths(t *testing.T, root string) map[string]bool {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", root,
		"ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatalf("list the files of the checkout: %v", err)
	}

	paths := map[string]bool{}

	for file := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
		paths[file] = false

		for dir := path.Dir(file); dir != "."; dir = path.Dir(dir) {
			paths[dir] = true
		}
	}

	return paths
}

// readerPaths walks the roots of one reader and returns each path its files
// name, with the files that name it, and the number of files it read.
func readerPaths(t *testing.T, root string, reader areaReader) (map[string][]string, int) {
	t.Helper()

	paths := map[string][]string{}
	files := 0

	for _, base := range reader.roots {
		visit := func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			rel := filepath.ToSlash(strings.TrimPrefix(file, root+string(filepath.Separator)))

			if entry.IsDir() {
				if skippedDirs[entry.Name()] || slices.Contains(reader.skip, rel) {
					return filepath.SkipDir
				}

				return nil
			}

			body, readErr := os.ReadFile(file)
			if readErr != nil {
				return readErr
			}

			named := reader.names(rel, base, body)
			if named == nil {
				return nil
			}

			files++

			for _, target := range named {
				paths[target] = append(paths[target], rel)
			}

			return nil
		}

		if err := filepath.WalkDir(filepath.Join(root, base), visit); err != nil {
			t.Fatalf("walking %s: %v", base, err)
		}
	}

	return paths, files
}

// pathLike is a string that reads as a path in this tree.
var pathLike = regexp.MustCompile(`^[A-Za-z0-9_.@-]+(/[A-Za-z0-9_.@*-]+)+/?$`)

// cleanTarget turns a named path into one relative to the repository, or ""
// when it is not one.
func cleanTarget(name string) string {
	name = strings.TrimPrefix(name, "./")
	for strings.HasPrefix(name, "../") {
		name = strings.TrimPrefix(name, "../")
	}

	name = strings.TrimSuffix(name, "/")
	if !pathLike.MatchString(name) || strings.Contains(name, "*") {
		return ""
	}

	return name
}

// goPathNames reads the paths one Go file names: each string that reads as a
// path, and the string arguments of each filepath.Join and path.Join call,
// joined.
func goPathNames(file, _ string, body []byte) []string {
	if !strings.HasSuffix(file, ".go") {
		return nil
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), file, body, parser.SkipObjectResolution)
	if err != nil {
		return []string{}
	}

	var names []string

	ast.Inspect(parsed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return true
			}

			if text, unquoteErr := strconv.Unquote(value.Value); unquoteErr == nil {
				if target := cleanTarget(text); target != "" {
					names = append(names, target)
				}
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Join" {
				return true
			}

			var parts []string

			for _, arg := range value.Args {
				literal, isLiteral := arg.(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.STRING {
					continue
				}

				text, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr != nil || text == ".." || text == "." {
					continue
				}

				parts = append(parts, text)
			}

			if target := cleanTarget(path.Join(parts...)); len(parts) > 1 && target != "" {
				names = append(names, target)
			}
		}

		return true
	})

	return names
}

// quotedRelative matches a quoted path that starts with ../.
var quotedRelative = regexp.MustCompile("['\"`]((?:\\.\\./)+[^'\"`\\s?]*)")

// relativePathNames reads the paths a script, a config or a component names
// with ../, resolved against its folder and against its root. A name that
// stays inside the root is the reader's own file.
func relativePathNames(file, base string, body []byte) []string {
	switch path.Ext(file) {
	case ".ts", ".js", ".mjs", ".cjs", ".mts", ".svelte", ".json", ".astro":
	default:
		return nil
	}

	var names []string

	for _, match := range quotedRelative.FindAllStringSubmatch(string(body), -1) {
		for _, from := range []string{path.Dir(file), base} {
			resolved := path.Clean(path.Join(from, match[1]))
			if strings.HasPrefix(resolved, "..") || resolved == base || strings.HasPrefix(resolved, base+"/") {
				continue
			}

			if target := cleanTarget(resolved); target != "" {
				names = append(names, target)
			}
		}
	}

	return names
}
