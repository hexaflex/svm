// Package asm implements an assembler which turns a module and its dependencies into
// a binary program, ready for use on a VM.
package asm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hexaflex/svm/asm/ar"
	"github.com/hexaflex/svm/asm/parser"

	"github.com/pkg/errors"
)

type astCache struct {
	module string
	ast    *parser.AST
}

// BuildAST builds the full AST for the given module and its dependencies.
func BuildAST(importpath, module string) (*parser.AST, error) {
	var cache []astCache

	if err := buildAST(&cache, importpath, module, nil); err != nil {
		return nil, err
	}

	// Merge all parsed modules into a single AST.
	const Sep = string(os.PathSeparator)
	ast := parser.NewAST()

	for _, v := range cache {
		nodes := v.ast.Nodes()
		pos := nodes.Position()

		if nodes.Len() > 0 {
			pos = nodes.At(0).Position()
		}

		module := filepath.Clean(strings.Replace(v.module, "/", Sep, -1))
		components := strings.Split(module, Sep)
		scopeEnd := parser.NewValue(pos, parser.ScopeEnd, "")

		for i := len(components) - 1; i >= 0; i-- {
			name := components[i]
			nodes.InsertAt(0, parser.NewValue(pos, parser.ScopeBegin, name))
			nodes.Append(scopeEnd)
		}

		ast.Merge(v.ast)
	}

	return ast, nil
}

// Build builds a binary program from the given module and its dependencies.
// It optionally emits debug symbols. The module and its dependencies are expected
// to have their sources located in the given import root directory.
func Build(importpath, module string, debug bool) (*ar.Archive, error) {
	ast, err := BuildAST(importpath, module)
	if err != nil {
		return nil, err
	}

	asm := newAssembler(debug)
	return asm.assemble(ast, module)
}

// buildAST constructs an AST from all the module's sources and its dependencies.
// It ensures the module and its dependencies do not contain any circular references.
func buildAST(cache *[]astCache, importpath, module string, importChain []string) error {
	module = strings.ToLower(module)

	if containsCache(*cache, module) {
		return nil // Already parsed.
	}

	if containsString(importChain, module) {
		return fmt.Errorf("circular reference to module %q detected", module)
	}

	importChain = append(importChain, module)

	// Find all the source files for the given module.
	sources, err := collateSources(importpath, module)
	if err != nil {
		return err
	}

	// Load them all into an AST.
	ast := parser.NewAST()

	for _, file := range sources {
		if err := ast.ParseFile(file); err != nil {
			return err
		}
	}

	*cache = append(*cache, astCache{
		module: module,
		ast:    ast,
	})

	return testAndBuildImports(cache, ast, importpath, importChain)
}

// testAndBuildImports finds all import statenents in the given AST and checks them recursively.
// If valid, parses them into the AST.
func testAndBuildImports(cache *[]astCache, ast *parser.AST, importpath string, importChain []string) error {
	return ast.Nodes().Each(func(_ int, n parser.Node) error {
		if n.Type() != parser.Instruction {
			return nil
		}

		instr := n.(*parser.List)
		name := instr.At(0).(*parser.Value).Value
		if !strings.EqualFold(name, "import") {
			return nil
		}

		var path string
		switch instr.Len() {
		case 2:
			expr := instr.At(1).(*parser.List)
			path = expr.At(0).(*parser.Value).Value
		case 3:
			expr := instr.At(2).(*parser.List)
			path = expr.At(0).(*parser.Value).Value
		default:
			return parser.NewError(instr.Position(), "invalid import path")
		}

		if err := buildAST(cache, importpath, path, importChain); err != nil {
			if _, ok := err.(*parser.Error); ok {
				return err
			}
			return parser.NewError(instr.Position(), err.Error())
		}

		return nil
	})
}

// containsString returns true if set contains v.
func containsString(set []string, v string) bool {
	for _, sv := range set {
		if sv == v {
			return true
		}
	}
	return false
}

// containsCache returns true if set contains an entry with the given module name.
func containsCache(set []astCache, v string) bool {
	for _, cv := range set {
		if cv.module == v {
			return true
		}
	}
	return false
}

// collateSources returns a list of all the source files associated with
// the given module. These will be absolute paths and are expected to be
// located in the import root directory.
func collateSources(importpath, module string) ([]string, error) {
	path := filepath.Join(importpath, module)

	fd, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to locate source directory for module %q", module)
	}

	files, err := fd.Readdirnames(-1)
	fd.Close()

	if err != nil {
		return nil, errors.Wrapf(err, "failed to read file names for moduke %q", module)
	}

	// remove entries with invalid file extensions.
	// Ensure the rest are absolute paths.
	for i := 0; i < len(files); i++ {
		if isSourceFile(files[i]) {
			files[i] = filepath.Join(path, files[i])
			files[i], _ = filepath.Abs(files[i])
			continue
		}

		copy(files[i:], files[i+1:])
		files = files[:len(files)-1]
		i--
	}

	return files, nil
}

// isSourceFile returns true if file has an expected file extension.
func isSourceFile(file string) bool {
	ext := filepath.Ext(file)
	switch strings.ToLower(ext) {
	case ".svm", ".asm":
		return true
	}
	return false
}
