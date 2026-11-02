// Package archtest enforces the dependency rules of the modular monolith:
//
//  1. domain does not import gin, pgx, net/http or database/sql.
//  2. A module does not import another module's inner packages: only its root
//     package (module.go) or its api/ package.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const modulePath = "github.com/AtmosferaSoltec/atm-km-api"

var forbiddenInDomain = []string{
	"github.com/gin-gonic/",
	"github.com/jackc/pgx",
	"net/http",
	"database/sql",
}

type goFile struct {
	path    string // relative to internal/, with /
	imports []string
}

func internalFiles(t *testing.T) []goFile {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var files []goFile
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		file := goFile{path: filepath.ToSlash(rel)}
		for _, spec := range parsed.Imports {
			value, _ := strconv.Unquote(spec.Path.Value)
			file.imports = append(file.imports, value)
		}
		files = append(files, file)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files
}

func domainViolations(files []goFile) []string {
	var problems []string
	for _, file := range files {
		if !strings.Contains(file.path, "/domain/") {
			continue
		}
		for _, imported := range file.imports {
			for _, forbidden := range forbiddenInDomain {
				if strings.HasPrefix(imported, forbidden) {
					problems = append(problems, file.path+" imports "+imported+": domain must stay plain Go")
				}
			}
		}
	}
	return problems
}

func moduleViolations(files []goFile) []string {
	prefix := modulePath + "/internal/modules/"
	var problems []string
	for _, file := range files {
		owner, ok := moduleOf(file.path)
		if !ok {
			continue
		}
		for _, imported := range file.imports {
			rest, ok := strings.CutPrefix(imported, prefix)
			if !ok {
				continue
			}
			other, inner, _ := strings.Cut(rest, "/")
			if other == owner || inner == "" || inner == "api" {
				continue
			}
			problems = append(problems, file.path+" imports "+imported+": use the public API of module "+other)
		}
	}
	return problems
}

// moduleOf tells which module a file under internal/modules/<module>/... belongs to.
func moduleOf(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "modules/")
	if !ok {
		return "", false
	}
	module, _, _ := strings.Cut(rest, "/")
	return module, true
}

func TestTheCodeFollowsTheDependencyRules(t *testing.T) {
	files := internalFiles(t)
	assert.NotEmpty(t, files)
	assert.Empty(t, domainViolations(files))
	assert.Empty(t, moduleViolations(files))
}

func TestTheRulesCatchViolations(t *testing.T) {
	files := []goFile{
		{path: "modules/trips/domain/trip.go", imports: []string{"github.com/jackc/pgx/v5"}},
		{path: "modules/trips/domain/billing.go", imports: []string{"github.com/shopspring/decimal"}},
		{path: "modules/home/app/service.go", imports: []string{modulePath + "/internal/modules/trips/adapters/postgres"}},
		{path: "modules/home/app/query.go", imports: []string{modulePath + "/internal/modules/trips"}},
		{path: "modules/home/app/port.go", imports: []string{modulePath + "/internal/modules/trips/api"}},
		{path: "modules/trips/app/use.go", imports: []string{modulePath + "/internal/modules/trips/domain"}},
	}
	assert.Len(t, domainViolations(files), 1)
	assert.Len(t, moduleViolations(files), 1)
}
