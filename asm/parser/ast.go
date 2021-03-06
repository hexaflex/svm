package parser

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hexaflex/svm/arch"
)

// AST defines an Abstract Syntax Tree for SVM sources.
type AST struct {
	nodes *List // AST node tree.
}

//NewAST creates a new, empty AST.
func NewAST() *AST {
	return &AST{
		nodes: NewList(Position{}, 0),
	}
}

// Nodes returns the top level node list.
func (a *AST) Nodes() *List {
	return a.nodes
}

// SetNodes sets the top level node list.
func (a *AST) SetNodes(set *List) {
	a.nodes = set
}

// ParseFile parses the given file into the AST.
// Parsing the same file more than once is not an error and is silently ignored.
func (a *AST) ParseFile(filename string) error {
	fd, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer fd.Close()
	return a.Parse(fd, filename)
}

// Parse parses the given stream into the AST. The filename is used to provide
// source context. Parsing the same file more than once is not an error and is
// silently ignored.
func (a *AST) Parse(r io.Reader, filename string) error {
	filename, err := a.verifyFilename(filename)
	if err != nil {
		return err
	}

	stack := []*List{a.nodes}

	return tokenize(r, filename, func(tt int, pos Position, value string) error {
		set := stack[len(stack)-1]

		switch tt {
		case tokInstructionBegin:
			var ntype Type

			switch strings.ToLower(value) {
			case "const":
				ntype = Constant
			default:
				ntype = Instruction
			}

			n := NewList(pos, ntype)
			n.Append(NewValue(pos, Ident, value))
			set.Append(n)
			stack = append(stack, n)

		case tokMacroBegin:
			n := NewList(pos, Macro)
			n.Append(NewValue(pos, Ident, value))
			set.Append(n)
			stack = append(stack, n)

		case tokExpressionBegin:
			n := NewList(pos, Expression)
			set.Append(n)
			stack = append(stack, n)

		case tokIfBegin:
			n := NewList(pos, Conditional)
			set.Append(n)
			stack = append(stack, n)

		case tokTypeDescriptor:
			set.Append(NewValue(pos, TypeDescriptor, value))

		case tokBreakPoint:
			set.Append(NewValue(pos, BreakPoint, value))

		case tokLabel:
			set.Append(NewValue(pos, Label, value))

		case tokNumber:
			set.Append(NewValue(pos, Number, value))

		case tokOperator:
			set.Append(NewValue(pos, Operator, value))

		case tokIdent:
			if index := arch.RegisterIndex(value); index > -1 {
				// If this is a second address mode in a row, combine them together.
				// This happens with an expression like `[r0]`.
				if set.Len() > 0 && set.At(set.Len()-1).Type() == AddressMode {
					set.At(set.Len() - 1).(*Value).Value += "r"
				} else {
					set.Append(NewValue(pos, AddressMode, "r"))
				}

				set.Append(NewValue(pos, Number, strconv.Itoa(index)))
			} else {
				set.Append(NewValue(pos, Ident, value))
			}

		case tokAddressMode:
			// If this is a second address mode in a row, combine them together.
			// This happens with an expression like `[r0]`.
			if set.Len() > 0 && set.At(set.Len()-1).Type() == AddressMode {
				set.At(set.Len() - 1).(*Value).Value += value
			} else {
				set.Append(NewValue(pos, AddressMode, value))
			}

		case tokScopeBegin:
			set.Append(NewValue(pos, ScopeBegin, ""))

		case tokScopeEnd:
			set.Append(NewValue(pos, ScopeEnd, ""))

		case tokChar:
			value, err := strconv.Unquote(value)
			if err != nil {
				return NewError(pos, "invalid character literal %v", value)
			}

			r, _ := utf8.DecodeRuneInString(value)
			if r == utf8.RuneError {
				return NewError(pos, "invalid character literal %q", value)
			}
			set.Append(NewValue(pos, Number, strconv.Itoa(int(r))))

		case tokString:
			value, err := strconv.Unquote(value)
			if err != nil {
				return NewError(pos, "invalid string literal %v", value)
			}

			set.Append(NewValue(pos, String, value))

		case tokInstructionEnd, tokMacroEnd, tokExpressionEnd, tokIfEnd:
			stack[len(stack)-1] = nil
			stack = stack[:len(stack)-1]
		}

		return nil
	})
}

// verifyFilename returns filename after ensuring it is an absolute path and
// is otherwise valid. If the filename is empty, a new filename is generated somewhere
// in the system's TEMP directory.
func (a *AST) verifyFilename(filename string) (string, error) {
	if len(filename) > 0 {
		abs, err := filepath.Abs(filename)
		if err != nil {
			return filename, err
		}
		return abs, nil
	}

	return filename, nil
}

// String returns a human readable string representation of the node tree.
func (a *AST) String() string {
	var sb strings.Builder
	dumpNode(&sb, a.nodes, "")
	return sb.String()
}

func dumpNode(w io.Writer, n Node, indent string) {
	pos := n.Position()
	_, file := filepath.Split(pos.File)
	posStr := fmt.Sprintf("%s:%d:%d", file, pos.Line, pos.Col)

	switch t := n.(type) {
	case *Value:
		fmt.Fprintf(w, "%s%s %s(%q)\n", indent, posStr, t.Type(), t.Value)
	case *List:
		fmt.Fprintf(w, "%s%s %s {\n", indent, posStr, t.Type())

		t.Each(func(i int, n Node) error {
			dumpNode(w, n, indent+"   ")
			return nil
		})

		fmt.Fprintf(w, "%s}\n", indent)
	}
}
