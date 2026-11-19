package mailbox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// transactionalLogKeys are the only attributes transactional code may log: identifiers, states,
// and counts, never an address, a subject, a variable, or ciphertext.
var transactionalLogKeys = map[string]bool{
	"request_id": true, "organization_id": true, "message_id": true, "template_id": true, "mode": true,
	"status": true, "failure_code": true, "attempts": true, "duration_ms": true, "count": true,
	// event is a delivery event type (delivered, bounced, ...), not personal data.
	"event": true,
}

// slogAttributeConstructors build one attribute from a key and a value.
var slogAttributeConstructors = map[string]bool{
	"String": true, "Int": true, "Int64": true, "Uint64": true, "Float64": true, "Bool": true,
	"Time": true, "Duration": true, "Any": true, "Group": true,
}

// slogLogCalls take a message and then key-value pairs or attributes.
var slogLogCalls = map[string]int{
	"Debug": 1, "Info": 1, "Warn": 1, "Error": 1,
	"DebugContext": 2, "InfoContext": 2, "WarnContext": 2, "ErrorContext": 2,
}

func TestTransactionalLogsCarryNoPersonalData(t *testing.T) {
	files, err := filepath.Glob("transactional_*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	// Delivery events of transactional messages are recorded here too.
	files = append(files, "delivery_event.go")
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "slog" {
				return true
			}
			if slogAttributeConstructors[selector.Sel.Name] && len(call.Args) > 0 {
				checkLogKey(t, name, call.Args[0])
			}
			if first, isLog := slogLogCalls[selector.Sel.Name]; isLog {
				// Bare key-value pairs: every string literal in a key position is a key.
				for i := first; i < len(call.Args); i++ {
					if literal, ok := call.Args[i].(*ast.BasicLit); ok && literal.Kind == token.STRING {
						checkLogKey(t, name, literal)
						i++ // skip the value
					}
				}
			}
			return true
		})
	}
	if checked < 4 {
		t.Fatalf("checked %d transactional files; the glob found too few", checked)
	}
}

func checkLogKey(t *testing.T, file string, expression ast.Expr) {
	t.Helper()
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		t.Errorf("%s: a log attribute key is not a string literal: %T", file, expression)
		return
	}
	key, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Errorf("%s: unquote %s: %v", file, literal.Value, err)
		return
	}
	if !transactionalLogKeys[key] {
		t.Errorf("%s: log attribute %q is not one of the allowed keys", file, key)
	}
}
