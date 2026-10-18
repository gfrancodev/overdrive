// Command mutants lists the mutations scripts/mutation.py applies to Go source files, one JSON object per line.
// It only reads the files: offsets point at the bytes to replace. Standard library only, so `go run` works from
// the repository root without a module.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

type mutant struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	// FuncStart and FuncEnd are the lines of the enclosing function, for lines coverage does not instrument
	// (a switch case's expression, for one).
	FuncStart int `json:"func_start,omitempty"`
	FuncEnd   int `json:"func_end,omitempty"`
}

// replacements maps an operator to the operators it is swapped for, and the kind of each swap.
var replacements = map[token.Token][][2]string{
	token.LSS:  {{"<=", "CONDITIONALS_BOUNDARY"}, {">=", "CONDITIONALS_NEGATION"}},
	token.LEQ:  {{"<", "CONDITIONALS_BOUNDARY"}, {">", "CONDITIONALS_NEGATION"}},
	token.GTR:  {{">=", "CONDITIONALS_BOUNDARY"}, {"<=", "CONDITIONALS_NEGATION"}},
	token.GEQ:  {{">", "CONDITIONALS_BOUNDARY"}, {"<", "CONDITIONALS_NEGATION"}},
	token.EQL:  {{"!=", "CONDITIONALS_NEGATION"}},
	token.NEQ:  {{"==", "CONDITIONALS_NEGATION"}},
	token.LAND: {{"||", "INVERT_LOGICAL"}},
	token.LOR:  {{"&&", "INVERT_LOGICAL"}},
	token.ADD:  {{"-", "ARITHMETIC_BASE"}},
	token.SUB:  {{"+", "ARITHMETIC_BASE"}},
	token.MUL:  {{"/", "ARITHMETIC_BASE"}},
	token.QUO:  {{"*", "ARITHMETIC_BASE"}},
	token.REM:  {{"*", "ARITHMETIC_BASE"}},
}

// stringy reports an operand that is visibly a string, where + concatenates and - would not compile.
func stringy(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING || v.Kind == token.CHAR
	case *ast.BinaryExpr:
		return v.Op == token.ADD && (stringy(v.X) || stringy(v.Y))
	case *ast.ParenExpr:
		return stringy(v.X)
	}
	return false
}

func list(path string) ([]mutant, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []mutant
	var fn *ast.FuncDecl
	add := func(pos token.Pos, from, to, kind string) {
		p := fset.Position(pos)
		m := mutant{File: path, Line: p.Line, Column: p.Column, Offset: p.Offset, Length: len(from), From: from, To: to, Kind: kind}
		if fn != nil && fn.Pos() <= pos && pos < fn.End() {
			m.FuncStart, m.FuncEnd = fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
		}
		out = append(out, m)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			fn = v
		case *ast.GenDecl:
			// Constants and types are not behavior worth mutating; their values are checked where they are used.
			return v.Tok != token.CONST && v.Tok != token.TYPE && v.Tok != token.IMPORT
		case *ast.BinaryExpr:
			if v.Op == token.ADD && (stringy(v.X) || stringy(v.Y)) {
				return true
			}
			for _, r := range replacements[v.Op] {
				add(v.OpPos, v.Op.String(), r[0], r[1])
			}
		case *ast.IncDecStmt:
			if v.Tok == token.INC {
				add(v.TokPos, "++", "--", "INCREMENT_DECREMENT")
			} else {
				add(v.TokPos, "--", "++", "INCREMENT_DECREMENT")
			}
		case *ast.UnaryExpr:
			switch v.Op {
			case token.NOT:
				add(v.OpPos, "!", "", "REMOVE_NOT")
			case token.SUB:
				add(v.OpPos, "-", "", "INVERT_NEGATIVES")
			}
		case *ast.Ident:
			switch v.Name {
			case "true":
				add(v.NamePos, "true", "false", "BOOLEAN_LITERAL")
			case "false":
				add(v.NamePos, "false", "true", "BOOLEAN_LITERAL")
			}
		}
		return true
	})
	return out, nil
}

func main() {
	enc := json.NewEncoder(os.Stdout)
	for _, path := range os.Args[1:] {
		ms, err := list(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, m := range ms {
			if err := enc.Encode(m); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
}
