package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/token"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"unicode"

	"github.com/Bo0mer/gentools/pkg/astgen"
	"github.com/Bo0mer/gentools/pkg/resolution"
	"github.com/Bo0mer/gentools/pkg/transformation"
)

var supportedLoggers = []string{
	"go_kit_log",
	"zap",
}

func isLoggerSupported(loggerType string) bool {
	for _, val := range supportedLoggers {
		if val == loggerType {
			return true
		}
	}

	return false
}

func init() {
	flag.Usage = func() {
		var out io.Writer = os.Stdout

		fmt.Fprintln(out, "A tool that generates logging wrappers for interfaces.")
		fmt.Fprintf(out, "Usage: %s [-h] SOURCE_DIR INTERFACE_NAME\n", path.Base(os.Args[0]))
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "  Arguments:")
		fmt.Fprintln(out, "    SOURCE_DIR       Path to the file containing the interface")
		fmt.Fprintln(out, "    INTERFACE_NAME   Name of the interface which will be wrapped")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "  Options:")
		fmt.Fprintln(out, "    -h               Print this text and exit")
		fmt.Fprintln(out, "")
	}
}

func parseArgs() (sourceDir, interfaceName string, loggerType string, err error) {
	flag.Parse()
	if flag.NArg() < 2 {
		return "", "", "", errors.New("not enough arguments provided")
	}

	sourceDir = flag.Arg(0)
	sourceDir, err = filepath.Abs(sourceDir)
	if err != nil {
		return "", "", "", fmt.Errorf("error determining absolute path to source directory: %v", err)
	}
	interfaceName = flag.Arg(1)

	loggerType = supportedLoggers[0]
	if flag.NArg() == 3 {
		loggerType = flag.Arg(2)
		if !isLoggerSupported(loggerType) {
			return "", "", "", fmt.Errorf("unsupported logger type: %s", loggerType)
		}
	}

	return sourceDir, interfaceName, loggerType, nil
}

func main() {
	sourceDir, interfaceName, loggerType, err := parseArgs()
	if err != nil {
		log.Fatal(err)
	}

	sourcePkgPath, err := dirToImport(sourceDir)
	if err != nil {
		log.Fatalf("error resolving import path of source directory: %v", err)
	}
	targetPkg := path.Base(sourcePkgPath) + "mws"

	locator := resolution.NewLocator()

	context := resolution.NewSingleLocationContext(sourcePkgPath)
	d, err := locator.FindIdentType(context, ast.NewIdent(interfaceName))
	if err != nil {
		log.Fatal(err)
	}

	typeName := fmt.Sprintf("errorLogging%s", interfaceName)

	model := newModel(sourcePkgPath, interfaceName, loggerType, typeName, targetPkg)
	generator := astgen.Generator{
		Model:    model,
		Locator:  locator,
		Resolver: resolution.NewResolver(model, locator),
	}

	err = generator.ProcessInterface(d)
	if err != nil {
		log.Fatal(err)
	}

	targetPkgPath := filepath.Join(sourceDir, targetPkg)
	if err := os.MkdirAll(targetPkgPath, 0777); err != nil {
		log.Fatalf("error creating target package directory: %v", err)
	}

	fd, err := os.Create(filepath.Join(targetPkgPath, filename(interfaceName)))
	if err != nil {
		log.Fatalf("error creating output source file: %v", err)
	}
	defer fd.Close()

	err = model.WriteSource(fd)
	if err != nil {
		log.Fatal(err)
	}

	wd, _ := os.Getwd()
	path, err := filepath.Rel(wd, fd.Name())
	if err != nil {
		path = fd.Name()
	}
	fmt.Printf("Wrote logging implementation of %q to %q\n", sourcePkgPath+"."+interfaceName, path)
}

func filename(interfaceName string) string {
	return fmt.Sprintf("logging_%s.go", toSnakeCase(interfaceName))
}

func dirToImport(p string) (string, error) {
	pkg, err := build.ImportDir(p, build.FindOnly)
	if err != nil {
		return "", err
	}
	return pkg.ImportPath, nil
}
func importToDir(imp string) (string, error) {
	pkg, err := build.Import(imp, "", build.FindOnly)
	if err != nil {
		return "", err
	}
	return pkg.Dir, nil
}

type constructorBuilder struct {
	logPackageName       string
	interfacePackageName string
	interfaceName        string
	contextPackageName   string

	loggerInfo			 *loggerInfo
}

func newConstructorBuilder(packageName, interfaceName, contextPackageName string, loggerInfo *loggerInfo) *constructorBuilder {
	return &constructorBuilder{
		interfacePackageName: packageName,
		interfaceName:        interfaceName,
		contextPackageName:   contextPackageName,
		loggerInfo:			  loggerInfo,
	}
}

func (c *constructorBuilder) Build() ast.Decl {
	funcBody := &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent("f")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{&ast.CompositeLit{
					Type: fieldsFuncType(c.contextPackageName, c.loggerInfo),
					Elts: []ast.Expr{ast.NewIdent("return nil")},
				}},
			},
			&ast.IfStmt{
				Cond: &ast.BinaryExpr{
					X: &ast.CallExpr{
						Fun:  ast.NewIdent("len"),
						Args: []ast.Expr{ast.NewIdent("fields")},
					},
					Op: token.GTR,
					Y:  &ast.BasicLit{Kind: token.INT, Value: "0"},
				},
				Body: &ast.BlockStmt{List: []ast.Stmt{
					&ast.AssignStmt{
						Lhs: []ast.Expr{ast.NewIdent("f")},
						Tok: token.ASSIGN,
						Rhs: []ast.Expr{&ast.IndexExpr{X: ast.NewIdent("fields"), Index: &ast.BasicLit{Kind: token.INT, Value: "0"}}},
					},
				}},
			},
			&ast.ReturnStmt{
				Results: []ast.Expr{
					// TODO(borshukov): Find a better way to do this.
					ast.NewIdent(fmt.Sprintf("&errorLogging%s{next: next, logger: logger, fields: f}", c.interfaceName)),
				},
			},
		},
	}

	funcName := fmt.Sprintf("NewErrorLogging%s", c.interfaceName)
	return &ast.FuncDecl{
		Doc: &ast.CommentGroup{
			List: []*ast.Comment{&ast.Comment{
				Text: fmt.Sprintf("// %s creates new error logging middleware.", funcName),
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
					&ast.Field{
						Names: []*ast.Ident{ast.NewIdent("logger")},
						Type: c.loggerInfo.loggerType,
					},
					&ast.Field{
						Names: []*ast.Ident{ast.NewIdent("fields")},
						Type:  &ast.Ellipsis{Elt: fieldsFuncType(c.contextPackageName, c.loggerInfo)},
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

type LoggingMethodBuilder struct {
	methodConfig        *astgen.MethodConfig
	method              *astgen.Method
	contextPackageAlias string
	loggerInfo			*loggerInfo
}

func NewLoggingMethodBuilder(structName string, methodConfig *astgen.MethodConfig, contextPackageAlias string, loggerInfo *loggerInfo) *LoggingMethodBuilder {
	method := astgen.NewMethod(methodConfig.MethodName, "m", structName)

	return &LoggingMethodBuilder{
		methodConfig:        methodConfig,
		method:              method,
		contextPackageAlias: contextPackageAlias,
		loggerInfo:     	 loggerInfo,
	}
}

func (b *LoggingMethodBuilder) Build() ast.Decl {
	b.method.SetType(&ast.FuncType{
		Params: &ast.FieldList{
			List: b.methodConfig.MethodParams,
		},
		Results: &ast.FieldList{
			List: transformation.FieldsAsAnonymous(b.methodConfig.MethodResults),
		},
	})

	// Add method invocation:
	//   result1, result2 := m.next.Method(arg1, arg2)
	methodInvocation := NewMethodInvocation(b.methodConfig)
	methodInvocation.SetReceiver(&ast.SelectorExpr{
		X:   ast.NewIdent("m"), // receiver name
		Sel: ast.NewIdent("next"),
	})
	b.method.AddStatement(methodInvocation.Build())

	// Log if an error has occurred.
	n := len(b.methodConfig.MethodResults)
	if n > 0 {
		last := b.methodConfig.MethodResults[n-1]
		if id, ok := last.Type.(*ast.Ident); ok && id.Name == "error" {
			switch b.loggerInfo.name {
			case "go_kit_log":
				b.method.AddStatement(b.conditionalLogMessageStatementKitLog(b.methodConfig.MethodName, last.Names[0].Name))
			case "zap":
				b.method.AddStatement(b.conditionalLogMessageStatementZap(b.methodConfig.MethodName, last.Names[0].Name))
			}
		}
	}

	// Add return statement
	//   return result1, result2
	returnResults := NewReturnResults(b.methodConfig)
	b.method.AddStatement(returnResults.Build())

	return b.method.Build()
}

func (b *LoggingMethodBuilder) contextArgName() (string, bool) {
	if len(b.methodConfig.MethodParams) == 0 {
		return "", false
	}

	p1 := b.methodConfig.MethodParams[0]
	if sel, ok := p1.Type.(*ast.SelectorExpr); ok {
		if sel.Sel.String() == "Context" {
			if id, ok := sel.X.(*ast.Ident); ok && id.String() == b.contextPackageAlias {
				return p1.Names[0].Name, true
			}
		}
	}

	return "", false
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
	resultSelectors := []ast.Expr{}
	for _, result := range m.method.MethodResults {
		resultSelectors = append(resultSelectors, ast.NewIdent(result.Names[0].String()))
	}

	paramSelectors := []ast.Expr{}
	for _, param := range m.method.MethodParams {
		paramSelectors = append(paramSelectors, ast.NewIdent(param.Names[0].String()))
	}

	callExpr := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   m.receiver,
			Sel: ast.NewIdent(m.method.MethodName),
		},
		Args: paramSelectors,
	}

	if m.method.HasResults() {
		return &ast.AssignStmt{
			Lhs: resultSelectors,
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				callExpr,
			},
		}
	}

	return &ast.ExprStmt{X: callExpr}
}

type ReturnResults struct {
	method *astgen.MethodConfig
}

func NewReturnResults(m *astgen.MethodConfig) *ReturnResults {
	return &ReturnResults{m}
}

func (r *ReturnResults) Build() ast.Stmt {
	resultSelectors := []ast.Expr{}
	for _, result := range r.method.MethodResults {
		resultSelectors = append(resultSelectors, ast.NewIdent(result.Names[0].String()))
	}

	return &ast.ReturnStmt{
		Results: resultSelectors,
	}
}

type startTimeRecorder struct {
	timePackageAlias string
}

func RecordStartTime(timePackageAlias string) *startTimeRecorder {
	return &startTimeRecorder{timePackageAlias}
}

func (r *startTimeRecorder) Build() ast.Stmt {
	callExpr := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent(r.timePackageAlias),
			Sel: ast.NewIdent("Now"),
		},
	}

	return &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent("_start")},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{
			callExpr,
		},
	}
}

func toSnakeCase(in string) string {
	runes := []rune(in)

	var out []rune
	for i := 0; i < len(runes); i++ {
		if i > 0 && (unicode.IsUpper(runes[i]) || unicode.IsNumber(runes[i])) && ((i+1 < len(runes) && unicode.IsLower(runes[i+1])) || unicode.IsLower(runes[i-1])) {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(runes[i]))
	}

	return string(out)
}
