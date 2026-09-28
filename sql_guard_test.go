package platform_test

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// pending are the places that still send text of SQL, each with the number
// of such calls and the reason — the list only shrinks (the test harness's
// own connection excepted). Key: path:function ("path:var" for a package-level
// declaration).
var pending = map[string]struct {
	n   int
	why string
}{
	"workflow/workflow.go:Routes":  {1, "view api.event_type — read by function once db-platform has list_event_type (1.2.31 has list_state_type only)"},
	"lib/rest/rest.go:Rows":        {1, "deprecated: kept for the projects that have not moved to pgtx.Call"},
	"lib/rest/rest.go:RowsOf":      {1, "deprecated, over Rows"},
	"lib/rest/rest.go:RowsHandler": {1, "deprecated, over RowsOf"},
	"lib/rest/rest.go:RowHandler":  {1, "deprecated: kept for the projects that have not moved to pgtx.Call"},

	"internal/resttest/resttest.go:Start": {2, "mints and signs out a test session on the administrator's DSN — the test's own connection, never the service's; stays"},
	"lib/pgtx/pgtxtest/pgtxtest.go:Login": {4, "mints a test session with a database-issued token on the administrator's DSN (a connection the caller passes) — the test's own, never the service's; stays"},
}

// Under the daemon role there is no text of SQL to send over schema api —
// every package reaches the database through pgtx.Call. A new raw query
// anywhere outside the files of lib/pgtx (its subpackages included) is red
// here, not a 500 on the stand.
func TestNoTextOfSQLOutsidePgtx(t *testing.T) {
	// methods that take (ctx, sql, …) on pgx, pgconn or database/sql
	raw := map[string]bool{"Query": true, "QueryRow": true, "Exec": true, "SendBatch": true, "CopyFrom": true,
		"Prepare": true, "ExecParams": true, "QueryContext": true, "QueryRowContext": true, "ExecContext": true}
	deprecated := map[string]bool{"Rows": true, "RowsHandler": true, "RowsOf": true, "RowHandler": true}
	fset := gotoken.NewFileSet()
	count := map[string]int{}
	var bad []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(path, ".") && path != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// lib/pgtx itself is where the text lives; its subpackages are not
		// exempt by being under it (pgtxtest is a package of its own)
		if filepath.ToSlash(filepath.Dir(path)) == "lib/pgtx" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		file := filepath.ToSlash(path)
		inRest := filepath.ToSlash(filepath.Dir(path)) == "lib/rest"
		// the name lib/rest is imported under in this file, alias included
		restName := ""
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); strings.HasSuffix(p, "/lib/rest") {
				restName = "rest"
				if imp.Name != nil {
					restName = imp.Name.Name
				}
			}
		}
		check := func(where string, n ast.Node) {
			ast.Inspect(n, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := ""
				switch fn := call.Fun.(type) {
				case *ast.SelectorExpr:
					pkg, _ := fn.X.(*ast.Ident)
					switch {
					case raw[fn.Sel.Name] && len(call.Args) >= 2: // (ctx, sql, …) — not url.Values' Query()
						name = fn.Sel.Name
					case deprecated[fn.Sel.Name] && pkg != nil && restName != "" && pkg.Name == restName:
						name = "rest." + fn.Sel.Name
					}
				case *ast.Ident:
					if inRest && deprecated[fn.Name] {
						name = fn.Name
					}
				}
				if name == "" {
					return true
				}
				if _, ok := pending[where]; ok {
					count[where]++
					return true
				}
				bad = append(bad, fset.Position(call.Pos()).String()+" "+where+": "+name)
				return true
			})
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				check(file+":"+d.Name.Name, d)
			case *ast.GenDecl:
				check(file+":var", d)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("text of SQL outside lib/pgtx — use pgtx.Call: %s", b)
	}
	// the count is exact: a new raw call inside a pending function is red too,
	// and a pending entry that sends less is lowered, so the list only shrinks
	for where, p := range pending {
		if count[where] != p.n {
			t.Errorf("pending %s: %d calls sending SQL, the list says %d (%s)", where, count[where], p.n, p.why)
		}
	}
}
