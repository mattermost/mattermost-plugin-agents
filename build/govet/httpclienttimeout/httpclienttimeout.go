// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package httpclienttimeout reports net/http.Client literals without a Timeout.
package httpclienttimeout

import (
	"go/ast"
	"go/constant"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const name = "httpclienttimeout"

var Analyzer = &analysis.Analyzer{
	Name: name,
	Doc: "report net/http.Client composite literals without a Timeout field outside _test.go files; " +
		"suppress a legitimate case with a same-line `//nolint:" + name + " // <reason>` comment",
	Run: run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if strings.HasSuffix(pass.Fset.File(file.Pos()).Name(), "_test.go") {
			continue
		}

		suppressed := suppressedLines(pass, file)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isHTTPClient(pass.TypesInfo.TypeOf(lit)) || hasTimeout(pass, lit) {
				return true
			}
			if suppressed[pass.Fset.Position(lit.Pos()).Line] {
				return true
			}
			pass.Reportf(lit.Pos(), "http.Client without Timeout; set one, or suppress with a reason if a context deadline bounds every request")
			return true
		})
	}
	return nil, nil
}

// isHTTPClient also accepts *http.Client because go/types records that type
// for a type-elided element of a []*http.Client literal.
func isHTTPClient(t types.Type) bool {
	if t == nil {
		return false
	}
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "net/http" && obj.Name() == "Client"
}

// hasTimeout treats a constant Timeout of zero or less as missing, since
// http.Client applies no deadline for it.
func hasTimeout(pass *analysis.Pass, lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Timeout" {
			value := pass.TypesInfo.Types[kv.Value].Value
			return value == nil || constant.Sign(value) > 0
		}
	}
	return false
}

func suppressedLines(pass *analysis.Pass, file *ast.File) map[int]bool {
	lines := map[int]bool{}
	for _, group := range file.Comments {
		for _, c := range group.List {
			directive, ok := strings.CutPrefix(c.Text, "//nolint:")
			if !ok {
				continue
			}
			linters, _, _ := strings.Cut(directive, " ")
			if slices.Contains(strings.Split(linters, ","), name) {
				lines[pass.Fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return lines
}
