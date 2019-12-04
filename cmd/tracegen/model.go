package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"io"

	"github.com/Bo0mer/gentools/pkg/astgen"
	"github.com/Bo0mer/gentools/pkg/transformation"
)

type model struct {
	interfacePath string
	interfaceName string
	fileBuilder   *astgen.File
	structName    string

	tracePackageAlias   string
	contextPackageAlias string
}

func newModel(interfacePath, interfaceName, structName, targetPkg string) *model {
	file := astgen.NewFile(targetPkg)
	strct := astgen.NewStruct(structName)
	file.AppendDeclaration(strct)

	m := &model{
		interfacePath: interfacePath,
		interfaceName: interfaceName,
		fileBuilder:   file,
		structName:    structName,
	}
	sourcePackageAlias := m.AddImport("", interfacePath)
	m.tracePackageAlias = m.AddImport("", "go.opencensus.io/trace")

	constructorBuilder := newConstructorBuilder(sourcePackageAlias, interfaceName)
	file.AppendDeclaration(constructorBuilder)

	strct.AddField("next", sourcePackageAlias, interfaceName)

	return m
}

func (m *model) WriteSource(w io.Writer) error {
	fmt.Fprintf(w, "// Code generated by tracegen. DO NOT EDIT.\n")
	astFile := m.fileBuilder.Build()

	if err := format.Node(w, token.NewFileSet(), astFile); err != nil {
		return err
	}
	return nil
}

func (m *model) AddImport(pkgName, location string) string {
	if location == "context" {
		m.contextPackageAlias = m.fileBuilder.AddImport(pkgName, location)
		return m.contextPackageAlias
	}
	return m.fileBuilder.AddImport(pkgName, location)
}

func (m *model) AddMethod(method *astgen.MethodConfig) error {
	fullMethodName := fmt.Sprintf("%s.%s.%s", m.interfacePath, m.interfaceName, method.MethodName)
	mmb := newTracingMethodBuilder(m.structName, method, m.tracePackageAlias, m.contextPackageAlias, fullMethodName)

	m.fileBuilder.AppendDeclaration(mmb)
	return nil
}

func (m *model) resolveInterfaceType(location, name string) *ast.SelectorExpr {
	alias := m.AddImport("", location)
	return &ast.SelectorExpr{
		X:   ast.NewIdent(alias),
		Sel: ast.NewIdent(name),
	}
}

type constructorBuilder struct {
	interfacePackageName string
	interfaceName        string
}

func newConstructorBuilder(packageName, interfaceName string) *constructorBuilder {
	return &constructorBuilder{
		interfacePackageName: packageName,
		interfaceName:        interfaceName,
	}
}

func (c *constructorBuilder) Build() ast.Decl {
	funcBody := &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.ReturnStmt{
				Results: []ast.Expr{
					// TODO(borshukov): Find a better way to do this.
					ast.NewIdent(fmt.Sprintf("&tracing%s{next}", c.interfaceName)),
				},
			},
		},
	}

	funcName := fmt.Sprintf("NewTracing%s", c.interfaceName)
	return &ast.FuncDecl{
		Doc: &ast.CommentGroup{
			List: []*ast.Comment{&ast.Comment{
				Text: fmt.Sprintf("// %s creates new tracing middleware.", funcName),
			}},
		},
		Name: ast.NewIdent(funcName),
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					&ast.Field{
						Names: []*ast.Ident{ast.NewIdent("next")},
						Type: &ast.SelectorExpr{
							X:   ast.NewIdent(c.interfacePackageName),
							Sel: ast.NewIdent(c.interfaceName),
						},
					},
				},
			},
			Results: &ast.FieldList{
				List: []*ast.Field{
					&ast.Field{
						Names: []*ast.Ident{ast.NewIdent("")},
						Type: &ast.SelectorExpr{
							X:   ast.NewIdent(c.interfacePackageName),
							Sel: ast.NewIdent(c.interfaceName),
						},
					},
				},
			},
		},
		Body: funcBody,
	}
}

type tracingMethodBuilder struct {
	fullMethodName      string
	methodConfig        *astgen.MethodConfig
	method              *astgen.Method
	tracePackageAlias   string
	contextPackageAlias string
}

func newTracingMethodBuilder(structName string, methodConfig *astgen.MethodConfig, tracePackageAlias, contextPackageAlias, fullMethodName string) *tracingMethodBuilder {
	method := astgen.NewMethod(methodConfig.MethodName, "m", structName)

	return &tracingMethodBuilder{
		fullMethodName:      fullMethodName,
		methodConfig:        methodConfig,
		method:              method,
		tracePackageAlias:   tracePackageAlias,
		contextPackageAlias: contextPackageAlias,
	}
}
func (b *tracingMethodBuilder) Build() ast.Decl {
	b.method.SetType(&ast.FuncType{
		Params: &ast.FieldList{
			List: b.methodConfig.MethodParams,
		},
		Results: &ast.FieldList{
			List: transformation.FieldsAsAnonymous(b.methodConfig.MethodResults),
		},
	})

	// If the first parameter is context, add tracing call.
	//   ctx, span := trace.StartSpan(ctx, "github.com/pkg.Component.Method")
	//   defer span.End()
	if len(b.methodConfig.MethodParams) > 0 {
		p1 := b.methodConfig.MethodParams[0]
		if sel, ok := p1.Type.(*ast.SelectorExpr); ok {
			if sel.Sel.String() == "Context" {
				if id, ok := sel.X.(*ast.Ident); ok && id.String() == b.contextPackageAlias {
					b.method.AddStatement(
						newTraceMethodInvocation(b.tracePackageAlias,
							p1.Names[0].Name, b.fullMethodName))

					b.method.AddStatement(newEndSpanStmt())

				}
			}
		}
	}

	// Add method invocation:
	//   result1, result2 := m.next.Method(arg1, arg2)
	methodInvocation := NewMethodInvocation(b.methodConfig)
	methodInvocation.SetReceiver(&ast.SelectorExpr{
		X:   ast.NewIdent("m"), // receiver name
		Sel: ast.NewIdent("next"),
	})
	b.method.AddStatement(methodInvocation.Build())

	return b.method.Build()
}

func newEndSpanStmt() ast.Stmt {
	callExpr := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent("_span"),
			Sel: ast.NewIdent("End"),
		},
	}

	return &ast.DeferStmt{Call: callExpr}
}

func newTraceMethodInvocation(tracePackageAlias, contextParamName, fullMethodName string) ast.Stmt {
	paramSelectors := []ast.Expr{
		ast.NewIdent(contextParamName),
		&ast.BasicLit{
			Kind:  token.STRING,
			Value: fmt.Sprintf("%q", fullMethodName),
		},
	}
	callExpr := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent(tracePackageAlias),
			Sel: ast.NewIdent("StartSpan"),
		},
		Args: paramSelectors,
	}

	resultSelectors := []ast.Expr{
		ast.NewIdent(contextParamName),
		ast.NewIdent("_span"),
	}

	return &ast.AssignStmt{
		Lhs: resultSelectors,
		Tok: token.DEFINE,
		Rhs: []ast.Expr{
			callExpr,
		},
	}
}

type MethodInvocation struct {
	receiver *ast.SelectorExpr
	method   *astgen.MethodConfig
}

func (m *MethodInvocation) SetReceiver(s *ast.SelectorExpr) {
	m.receiver = s
}

func NewMethodInvocation(method *astgen.MethodConfig) *MethodInvocation {
	return &MethodInvocation{method: method}
}

func (m *MethodInvocation) Build() ast.Stmt {
	var paramSelectors []ast.Expr
	var ellipsisPos token.Pos
	for _, param := range m.method.MethodParams {
		paramSelectors = append(paramSelectors, ast.NewIdent(param.Names[0].String()))
		if p, ok := param.Type.(*ast.Ellipsis); ok {
			ellipsisPos = p.Pos()
		}
	}

	callExpr := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   m.receiver,
			Sel: ast.NewIdent(m.method.MethodName),
		},
		Args:     paramSelectors,
		Ellipsis: ellipsisPos,
	}

	if m.method.HasResults() {
		return &ast.ReturnStmt{
			Results: []ast.Expr{callExpr},
		}
	}
	return &ast.ExprStmt{X: callExpr}
}
