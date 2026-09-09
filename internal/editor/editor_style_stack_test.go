package editor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A toolbar button that highlights itself while its mode is on is written as
// a push, the button, and a pop. If the pop's condition re-reads the mode
// instead of a value latched before the button, clicking the button flips the
// mode underneath it and the pop runs without its push. ImGui's color stack is
// global across contexts, so the unbalanced pop asserts ("Size > 0") and takes
// the process down — from a different window, usually, which makes it a
// miserable thing to track back to the button that caused it.
//
// Wireframe in the model editor was the fourth of these. This test is the
// guard: the condition on a bare PopStyleColor/PopStyleVar has to be a
// variable local to the function, never a global or a field read live.
func TestPopStyleGuardsAreLatchedLocals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			locals := functionLocals(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				stmt, ok := n.(*ast.IfStmt)
				if !ok || stmt.Init != nil || stmt.Else != nil || !isStylePop(stmt.Body) {
					return true
				}
				ident, ok := stmt.Cond.(*ast.Ident)
				if ok && locals[ident.Name] {
					return true
				}
				t.Errorf("%s: %s guards a style pop on %s, which is not a local latched "+
					"before the widget; clicking the widget can flip it and pop a color "+
					"that was never pushed", fset.Position(stmt.Pos()), fn.Name.Name,
					exprText(src, fset, stmt.Cond))
				return true
			})
		}
	}
}

// isStylePop reports whether a block is nothing but imgui.PopStyleColor or
// imgui.PopStyleVar calls — the shape a highlight guard has.
func isStylePop(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	for _, stmt := range body.List {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			return false
		}
		call, ok := expr.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "imgui" {
			return false
		}
		if sel.Sel.Name != "PopStyleColor" && sel.Sel.Name != "PopStyleVar" {
			return false
		}
	}
	return true
}

// functionLocals names everything declared inside a function: parameters,
// results, := bindings, var declarations and range variables.
func functionLocals(fn *ast.FuncDecl) map[string]bool {
	locals := map[string]bool{}
	addField := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			for _, name := range field.Names {
				locals[name.Name] = true
			}
		}
	}
	addField(fn.Recv)
	if fn.Type != nil {
		addField(fn.Type.Params)
		addField(fn.Type.Results)
	}
	addIdent := func(expr ast.Expr) {
		if ident, ok := expr.(*ast.Ident); ok {
			locals[ident.Name] = true
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, lhs := range s.Lhs {
					addIdent(lhs)
				}
			}
		case *ast.RangeStmt:
			addIdent(s.Key)
			addIdent(s.Value)
		case *ast.ValueSpec:
			for _, name := range s.Names {
				locals[name.Name] = true
			}
		case *ast.FuncLit:
			if s.Type != nil {
				addField(s.Type.Params)
				addField(s.Type.Results)
			}
		}
		return true
	})
	return locals
}

// exprText recovers the source text of an expression, for the error message.
func exprText(src []byte, fset *token.FileSet, expr ast.Expr) string {
	start := fset.Position(expr.Pos()).Offset
	end := fset.Position(expr.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return "the condition"
	}
	return string(src[start:end])
}
