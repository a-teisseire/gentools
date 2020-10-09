package main

import (
	"fmt"
	"go/ast"
	"go/token"
)

func (b *LoggingMethodBuilder) conditionalLogMessageStatementKitLog(methodName, errorResultName string) ast.Stmt {
	// If the first parameter is context.Context, get additional log
	// fields.
	var additionalFieldsStmt ast.Stmt = &ast.EmptyStmt{}
	var appendAdditionalFieldsStmt ast.Stmt = &ast.EmptyStmt{}
	if ctxArgName, ok := b.contextArgName(); ok {
		callExpr := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent("m"), // receiver name
				Sel: ast.NewIdent("fields"),
			},
			Args: []ast.Expr{ast.NewIdent(ctxArgName), ast.NewIdent(errorResultName)},
		}

		additionalFieldsStmt = &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent("_more")},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				callExpr,
			},
		}

		// if len(_more) > 0 {

		appendAdditionalFieldsStmt = &ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X: &ast.CallExpr{
					Fun:  ast.NewIdent("len"),
					Args: []ast.Expr{ast.NewIdent("_more")},
				},
				Op: token.GTR,
				Y:  ast.NewIdent("0"),
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					// _fields = append(_fields, _more...)
					&ast.AssignStmt{
						Lhs: []ast.Expr{ast.NewIdent("_fields")},
						Tok: token.ASSIGN,
						Rhs: []ast.Expr{
							&ast.CallExpr{
								Fun:  ast.NewIdent("append"),
								Args: []ast.Expr{ast.NewIdent("_fields"), ast.NewIdent("_more...")},
							},
						},
					},
				},
			},
		}
	}

	assignStmt := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent("_fields")},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{
			&ast.CompositeLit{
				Type: &ast.ArrayType{
					Elt: ast.NewIdent("interface{}"),
				},
				Elts: []ast.Expr{
					&ast.BasicLit{Kind: token.STRING, Value: `"method"`},
					&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", methodName)},
					&ast.BasicLit{Kind: token.STRING, Value: `"error"`},
					&ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   ast.NewIdent(errorResultName),
							Sel: ast.NewIdent("Error"),
						},
					},
				},
			},
		},
	}

	callLogExpr := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.SelectorExpr{X: ast.NewIdent("m"), Sel: ast.NewIdent("logger")},
			Sel: ast.NewIdent("Log"),
		},
		Args: []ast.Expr{
			ast.NewIdent("_fields..."),
		},
	}

	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{
			X:  ast.NewIdent(errorResultName),
			Op: token.NEQ,
			Y:  ast.NewIdent("nil"),
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				assignStmt,
				additionalFieldsStmt,
				appendAdditionalFieldsStmt,
				&ast.ExprStmt{X: callLogExpr}},
		},
	}
}