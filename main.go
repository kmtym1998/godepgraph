package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/build"
	"log"
	"os"
	"sort"
	"strings"
)

var (
	pkgs        map[string]*build.Package
	erroredPkgs map[string]bool
	ids         map[string]string

	ignored = map[string]bool{
		"C": true,
	}
	ignoredPrefixes []string
	onlyPrefixes    []string
	moduleName      string

	goModPath = flag.String("gomodpath", "./go.mod", "path to go.mod file")
	debugMode = flag.Bool("debug", false, "enable debug output")

	buildTags    []string
	buildContext = build.Default
)

const maxLevel = 256

func init() {
	flag.StringVar(goModPath, "p", "./go.mod", "path to go.mod file")
	flag.BoolVar(debugMode, "d", false, "enable debug output")
}

func main() {
	pkgs = make(map[string]*build.Package)
	erroredPkgs = make(map[string]bool)
	ids = make(map[string]string)
	flag.Parse()

	buildContext.BuildTags = buildTags

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to get cwd: %s", err)
	}

	goModFile, err := os.ReadFile(*goModPath)
	if err != nil {
		log.Fatalf("failed to read go.mod: %s", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(goModFile))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "module ") {
			moduleName = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("failed to read go.mod: %s", err)
	}
	if moduleName == "" {
		log.Fatal("failed to get module name")
	}

	if err := processPackage(cwd, "./", 0, "", true); err != nil {
		log.Fatal(err)
	}

	fmt.Println("digraph godep {")
	fmt.Print(`splines=ortho
nodesep=0.4
ranksep=0.8
node [shape="box",style="rounded,filled"]
edge [arrowsize="0.5"]
`)

	// sort packages
	pkgKeys := []string{}
	for k := range pkgs {
		pkgKeys = append(pkgKeys, k)
	}
	sort.Strings(pkgKeys)

	for _, pkgName := range pkgKeys {
		pkg := pkgs[pkgName]
		pkgId := getId(pkgName)

		if isIgnored(pkg) {
			continue
		}

		var color string
		switch {
		case pkg.Goroot:
			color = "palegreen"
		case len(pkg.CgoFiles) > 0:
			color = "darkgoldenrod1"
		case isVendored(pkg.ImportPath):
			color = "palegoldenrod"
		case hasBuildErrors(pkg):
			color = "red"
		default:
			color = "paleturquoise"
		}

		fmt.Printf("%s [label=\"%s\" color=\"%s\" URL=\"%s\" target=\"_blank\"];\n", pkgId, pkgName, color, pkgDocsURL(pkgName))

		// Don't render imports from packages in Goroot
		if pkg.Goroot {
			continue
		}

		for _, imp := range getImports(pkg) {
			impPkg := pkgs[imp]
			if impPkg == nil || isIgnored(impPkg) {
				continue
			}

			impId := getId(imp)
			fmt.Printf("%s -> %s;\n", pkgId, impId)
		}
	}
	fmt.Println("}")
}

func pkgDocsURL(pkgName string) string {
	return "https://godoc.org/" + pkgName
}

func processPackage(root string, pkgName string, level int, importedBy string, stopOnError bool) error {
	if level++; level > maxLevel {
		return nil
	}
	if ignored[pkgName] {
		return nil
	}

	pkg, buildErr := buildContext.Import(pkgName, root, 0)
	if buildErr != nil {
		if stopOnError {
			return fmt.Errorf("failed to import %s (imported at level %d by %s):\n%s", pkgName, level, importedBy, buildErr)
		}
	}

	if !(pkg.ImportPath == "./" || strings.HasPrefix(pkg.ImportPath, moduleName)) {
		return nil
	}

	if *debugMode {
		fmt.Fprintln(os.Stderr, "====================================")
		fmt.Fprintln(os.Stderr, "🦠 pkg.ImportPath:", pkg.ImportPath)
		fmt.Fprintln(os.Stderr, "🫚 root:", root)
		fmt.Fprintln(os.Stderr, "🫚 pkgName:", pkgName)
		fmt.Fprintln(os.Stderr, "🫚 importedBy:", importedBy)
		fmt.Fprintln(os.Stderr, "")
	}

	if buildErr != nil {
		erroredPkgs[pkgName] = true
	}

	pkgs[pkgName] = pkg

	// Don't worry about dependencies for stdlib packages
	if pkg.Goroot {
		return nil
	}

	for _, imp := range getImports(pkg) {
		if _, ok := pkgs[imp]; !ok {
			if err := processPackage(pkg.Dir, imp, level, pkgName, stopOnError); err != nil {
				return err
			}
		}
	}
	return nil
}

func getImports(pkg *build.Package) []string {
	allImports := pkg.Imports
	var imports []string
	found := make(map[string]struct{})
	for _, imp := range allImports {
		if imp == pkg.ImportPath {
			// Don't draw a self-reference when foo_test depends on foo.
			continue
		}
		if _, ok := found[imp]; ok {
			continue
		}
		found[imp] = struct{}{}
		imports = append(imports, imp)
	}
	return imports
}

func deriveNodeID(packageName string) string {
	//TODO: improve implementation?
	id := "\"" + packageName + "\""
	return id
}

func getId(name string) string {
	id, ok := ids[name]
	if !ok {
		id = deriveNodeID(name)
		ids[name] = id
	}
	return id
}

func hasPrefixes(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isIgnored(pkg *build.Package) bool {
	if len(onlyPrefixes) > 0 && !hasPrefixes(pkg.ImportPath, onlyPrefixes) {
		return true
	}

	return ignored[pkg.ImportPath] || hasPrefixes(pkg.ImportPath, ignoredPrefixes)
}

func hasBuildErrors(pkg *build.Package) bool {
	if len(erroredPkgs) == 0 {
		return false
	}

	v, ok := erroredPkgs[pkg.ImportPath]
	if !ok {
		return false
	}
	return v
}

func debug(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
}

func debugf(s string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, s, args...)
}

func isVendored(path string) bool {
	return strings.Contains(path, "/vendor/")
}
