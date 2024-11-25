package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	for _, cmdArg := range os.Args[1:] {
		packagePath := getPackagePath(cmdArg, cwd)
		if packagePath == "" {
			packagePath = searchPackagePathFromGoModCache(cmdArg)
			fmt.Printf("// `go list` failed, fallback to search GOMODCACHE: %s\n", packagePath)
		}
		if packagePath == "" {
			panic(fmt.Sprintf("module '%s' not found", cmdArg))
		}
		printExported(packagePath)
	}
}

func goModCache() string {
	gomodcache := os.Getenv("GOMODCACHE")
	if gomodcache != "" {
		return gomodcache
	}
	gopath := build.Default.GOPATH
	list := filepath.SplitList(gopath)
	if len(list) == 0 || list[0] == "" {
		return ""
	}
	return filepath.Join(list[0], "pkg/mod")
}

func searchPackagePathFromGoModCache(importPath string) string {
	pa := goModCache()
Loop:
	for _, name := range strings.Split(importPath, string(os.PathSeparator)) {
		list, err := os.ReadDir(pa)
		if err != nil {
			panic(err)
		}
		for _, f := range list {
			if f.IsDir() && (f.Name() == name || strings.HasPrefix(f.Name(), name+"@")) {
				pa = filepath.Join(pa, f.Name())
				continue Loop
			}
		}
		// no this module
		return ""
	}
	return pa
}

func getPackagePath(importPath, fromDir string) string {
	pack, err := build.Default.Import(importPath, fromDir, build.FindOnly)
	if err != nil {
		return ""
	}
	return pack.Dir
}

func printExported(dirpath string) {
	list, err := os.ReadDir(dirpath)
	if err != nil {
		panic(err)
	}
	slices.SortFunc(list, func(a, b fs.DirEntry) int {
		namea := a.Name()
		nameb := b.Name()
		if namea == nameb {
			return 0
		} else if namea < nameb {
			return -1
		} else {
			return 1
		}
	})

	fset := token.NewFileSet()
	for _, d := range list {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			continue
		}
		filepath := filepath.Join(dirpath, d.Name())
		if src, err := parser.ParseFile(fset, filepath, nil, 0); err == nil {
			if src.Name.Name == "main" { // ignore main package
				continue
			}
			printGoFileExport(filepath, src)
		}
	}
}

func printGoFileExport(filepath string, f *ast.File) {
	res := []string{}
	for _, xdecl := range f.Decls {
		switch decl := xdecl.(type) {
		case *ast.FuncDecl:
			if exported(decl) {
				res = append(res, formatFuncDecl(decl))
			}
		case *ast.GenDecl:
			s := formatGenDecl(decl)
			if s != "" {
				res = append(res, s)
			}
		}
	}
	if len(res) == 0 {
		return
	}
	printFileName(filepath)
	for _, line := range res {
		fmt.Println(line)
	}
	fmt.Println("")
}

func formatGenDecl(decl *ast.GenDecl) string {
	res := []string{}
	switch decl.Tok {
	case token.TYPE:
		for _, spec := range decl.Specs {
			sp, ok := spec.(*ast.TypeSpec)
			if ok && isUpper0(sp.Name.Name) {
				res = append(res, fmt.Sprintf("type %s %s", sp.Name.Name, formatType(sp.Type)))
			}
		}
	case token.VAR, token.CONST:
		key := "var"
		if decl.Tok == token.CONST {
			key = "const"
		}
		for _, spec := range decl.Specs {
			sp, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			typ := formatType(sp.Type)
			if typ != "" {
				typ += " "
			}
			for i, name := range sp.Names {
				if isUpper0(name.Name) {
					s := fmt.Sprintf("%s %s %s", key, name, typ)
					if len(sp.Values) > i {
						s += "= "
						s += formatType(sp.Values[i])
					}
					res = append(res, s)
				}
			}
		}
	}
	return strings.Join(res, "\n")
}

func printFileName(path string) {
	fmt.Printf("// %s:\n", filepath.Base(path))
}

func formatFuncDecl(decl *ast.FuncDecl) string {
	s := "func "
	if decl.Recv != nil {
		if len(decl.Recv.List) != 1 {
			return fmt.Sprintf("strange receiver for %s: %#v", decl.Name.Name, decl.Recv)
		}
		field := decl.Recv.List[0]
		if len(field.Names) == 0 {
			// function definition in interface (ignore)
			return ""
		} else if len(field.Names) != 1 {
			return fmt.Sprintf("strange receiver field for %s: %#v", decl.Name.Name, field)
		}
		s += fmt.Sprintf("(%s %s) ", field.Names[0], formatType(field.Type))
	}
	s += decl.Name.Name
	if decl.Type.TypeParams != nil {
		s += fmt.Sprintf("[%s]", formatFields(decl.Type.TypeParams))
	}
	s += fmt.Sprintf("(%s)", formatFields(decl.Type.Params))
	s += formatFuncResults(decl.Type.Results)
	return s
}

func formatFields(fields *ast.FieldList) string {
	s := ""
	for i, field := range fields.List {
		for j, name := range field.Names {
			s += name.Name
			if j != len(field.Names)-1 {
				s += ","
			}
			s += " "
		}
		s += formatType(field.Type)
		if i != len(fields.List)-1 {
			s += ", "
		}
	}
	return s
}

func formatType(typ ast.Expr) string {
	switch t := typ.(type) {
	case nil:
		return ""
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", formatType(t.X), t.Sel.Name)
	case *ast.StarExpr:
		return fmt.Sprintf("*%s", formatType(t.X))
	case *ast.ArrayType:
		return fmt.Sprintf("[%s]%s", formatType(t.Len), formatType(t.Elt))
	case *ast.Ellipsis:
		return "..." + formatType(t.Elt)
	case *ast.FuncType:
		return fmt.Sprintf("func(%s)%s", formatFields(t.Params), formatFuncResults(t.Results))
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", formatType(t.Key), formatType(t.Value))
	case *ast.ChanType:
		s := ""
		if t.Dir == 1 {
			s = "<-chan"
		} else if t.Dir == 2 {
			s = "chan<-"
		} else if t.Dir == 3 {
			s = "chan"
		}
		return fmt.Sprintf("%s %s", s, formatType(t.Value))
	case *ast.BasicLit:
		return t.Value
	case *ast.StructType:
		return "struct{}"
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.UnaryExpr:
		return t.Op.String() + formatType(t.X)
	case *ast.CompositeLit:
		// abandon fields in {}
		return formatType(t.Type) + "{}"
	case *ast.CallExpr:
		return formatType(t.Fun) + "()"
	case *ast.BinaryExpr:
		return fmt.Sprintf("%s %s %s", formatType(t.X), t.Op.String(), formatType(t.Y))
	case *ast.FuncLit:
		return formatType(t.Type)
	case *ast.IndexExpr:
		return fmt.Sprintf("%s[%s]", formatType(t.X), formatType(t.Index))
	case *ast.IndexListExpr:
		typ := []string{}
		for _, expr := range t.Indices {
			typ = append(typ, formatType(expr))
		}
		return fmt.Sprintf("%s[%s]", formatType(t.X), strings.Join(typ, ", "))
	case *ast.ParenExpr:
		return fmt.Sprintf("(%s)", formatType(t.X))
	case *ast.SliceExpr:
		s := formatType(t.X)
		s += "["
		if t.Low != nil {
			s += formatType(t.Low)
		}
		s += ":"
		if t.High != nil {
			s += formatType(t.High)
		}
		if t.Slice3 {
			s += ":"
		}
		if t.Max != nil {
			s += formatType(t.Max)
		}
		s += "]"
		return s
	case *ast.TypeAssertExpr:
		return fmt.Sprintf("%s.(%s)", formatType(t.X), formatType(t.Type))
	default:
		return fmt.Sprintf("unsupported type %#v", t)
	}
}

func formatFuncResults(fields *ast.FieldList) string {
	s := ""
	if fields != nil {
		s += " "
		needPar := len(fields.List) > 1 || (len(fields.List) == 1 && len(fields.List[0].Names) > 0)
		if needPar {
			s += "("
		}
		s += formatFields(fields)
		if needPar {
			s += ")"
		}
	}
	return s
}

func isUpper0(s string) bool {
	if strings.HasPrefix(s, "*") {
		return unicode.IsUpper([]rune(s)[1])
	}
	return unicode.IsUpper([]rune(s)[0])
}

func exported(decl *ast.FuncDecl) bool {
	if decl.Recv != nil {
		if len(decl.Recv.List) != 1 {
			panic(fmt.Errorf("strange receiver for %s: %#v", decl.Name.Name, decl.Recv))
		}
		field := decl.Recv.List[0]
		return isUpper0(formatType(field.Type)) && isUpper0(decl.Name.Name)
	}
	return isUpper0(decl.Name.Name)
}
