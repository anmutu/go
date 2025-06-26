// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syntax

import (
	"bytes"
	"flag"
	"fmt"
	"internal/testenv"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	fast   = flag.Bool("fast", false, "parse package files in parallel")
	verify = flag.Bool("verify", false, "verify idempotent printing")
	src_   = flag.String("src", "parser.go", "source file to parse")
	skip   = flag.String("skip", "", "files matching this regular expression are skipped by TestStdLib")
)

func TestParse(t *testing.T) {
	ParseFile(*src_, func(err error) { t.Error(err) }, nil, 0)
}

func TestVerify(t *testing.T) {
	ast, err := ParseFile(*src_, func(err error) { t.Error(err) }, nil, 0)
	if err != nil {
		return // error already reported
	}
	verifyPrint(t, *src_, ast)
}

func TestStdLib(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	var skipRx *regexp.Regexp
	if *skip != "" {
		var err error
		skipRx, err = regexp.Compile(*skip)
		if err != nil {
			t.Fatalf("invalid argument for -skip (%v)", err)
		}
	}

	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	start := time.Now()

	type parseResult struct {
		filename string
		lines    uint
	}

	goroot := testenv.GOROOT(t)

	results := make(chan parseResult)
	go func() {
		defer close(results)
		for _, dir := range []string{
			filepath.Join(goroot, "src"),
			filepath.Join(goroot, "misc"),
		} {
			if filepath.Base(dir) == "misc" {
				// cmd/distpack deletes GOROOT/misc, so skip that directory if it isn't present.
				// cmd/distpack also requires GOROOT/VERSION to exist, so use that to
				// suppress false-positive skips.
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					if _, err := os.Stat(filepath.Join(testenv.GOROOT(t), "VERSION")); err == nil {
						fmt.Printf("%s not present; skipping\n", dir)
						continue
					}
				}
			}

			walkDirs(t, dir, func(filename string) {
				if skipRx != nil && skipRx.MatchString(filename) {
					// Always report skipped files since regexp
					// typos can lead to surprising results.
					fmt.Printf("skipping %s\n", filename)
					return
				}
				if debug {
					fmt.Printf("parsing %s\n", filename)
				}
				ast, err := ParseFile(filename, nil, nil, 0)
				if err != nil {
					t.Error(err)
					return
				}
				if *verify {
					verifyPrint(t, filename, ast)
				}
				results <- parseResult{filename, ast.EOF.Line()}
			})
		}
	}()

	var count, lines uint
	for res := range results {
		count++
		lines += res.lines
		if testing.Verbose() {
			fmt.Printf("%5d  %s (%d lines)\n", count, res.filename, res.lines)
		}
	}

	dt := time.Since(start)
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	dm := float64(m2.TotalAlloc-m1.TotalAlloc) / 1e6

	fmt.Printf("parsed %d lines (%d files) in %v (%d lines/s)\n", lines, count, dt, int64(float64(lines)/dt.Seconds()))
	fmt.Printf("allocated %.3fMb (%.3fMb/s)\n", dm, dm/dt.Seconds())
}

func walkDirs(t *testing.T, dir string, action func(string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Error(err)
		return
	}

	var files, dirs []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			if strings.HasSuffix(entry.Name(), ".go") {
				path := filepath.Join(dir, entry.Name())
				files = append(files, path)
			}
		} else if entry.IsDir() && entry.Name() != "testdata" {
			path := filepath.Join(dir, entry.Name())
			if !strings.HasSuffix(path, string(filepath.Separator)+"test") {
				dirs = append(dirs, path)
			}
		}
	}

	if *fast {
		var wg sync.WaitGroup
		wg.Add(len(files))
		for _, filename := range files {
			go func(filename string) {
				defer wg.Done()
				action(filename)
			}(filename)
		}
		wg.Wait()
	} else {
		for _, filename := range files {
			action(filename)
		}
	}

	for _, dir := range dirs {
		walkDirs(t, dir, action)
	}
}

func verifyPrint(t *testing.T, filename string, ast1 *File) {
	var buf1 bytes.Buffer
	_, err := Fprint(&buf1, ast1, LineForm)
	if err != nil {
		panic(err)
	}
	bytes1 := buf1.Bytes()

	ast2, err := Parse(NewFileBase(filename), &buf1, nil, nil, 0)
	if err != nil {
		panic(err)
	}

	var buf2 bytes.Buffer
	_, err = Fprint(&buf2, ast2, LineForm)
	if err != nil {
		panic(err)
	}
	bytes2 := buf2.Bytes()

	if !bytes.Equal(bytes1, bytes2) {
		fmt.Printf("--- %s ---\n", filename)
		fmt.Printf("%s\n", bytes1)
		fmt.Println()

		fmt.Printf("--- %s ---\n", filename)
		fmt.Printf("%s\n", bytes2)
		fmt.Println()

		t.Error("printed syntax trees do not match")
	}
}

func TestIssue17697(t *testing.T) {
	_, err := Parse(nil, bytes.NewReader(nil), nil, nil, 0) // return with parser error, don't panic
	if err == nil {
		t.Errorf("no error reported")
	}
}

func TestParseFile(t *testing.T) {
	_, err := ParseFile("", nil, nil, 0)
	if err == nil {
		t.Error("missing io error")
	}

	var first error
	_, err = ParseFile("", func(err error) {
		if first == nil {
			first = err
		}
	}, nil, 0)
	if err == nil || first == nil {
		t.Error("missing io error")
	}
	if err != first {
		t.Errorf("got %v; want first error %v", err, first)
	}
}

// Make sure (PosMax + 1) doesn't overflow when converted to default
// type int (when passed as argument to fmt.Sprintf) on 32bit platforms
// (see test cases below).
var tooLarge int = PosMax + 1

func TestLineDirectives(t *testing.T) {
	// valid line directives lead to a syntax error after them
	const valid = "syntax error: package statement must be first"
	const filename = "directives.go"

	for _, test := range []struct {
		src, msg  string
		filename  string
		line, col uint // 1-based; 0 means unknown
	}{
		// ignored //line directives
		{"//\n", valid, filename, 2, 1},            // no directive
		{"//line\n", valid, filename, 2, 1},        // missing colon
		{"//line foo\n", valid, filename, 2, 1},    // missing colon
		{"  //line foo:\n", valid, filename, 2, 1}, // not a line start
		{"//  line foo:\n", valid, filename, 2, 1}, // space between // and line

		// invalid //line directives with one colon
		{"//line :\n", "invalid line number: ", filename, 1, 9},
		{"//line :x\n", "invalid line number: x", filename, 1, 9},
		{"//line foo :\n", "invalid line number: ", filename, 1, 13},
		{"//line foo:x\n", "invalid line number: x", filename, 1, 12},
		{"//line foo:0\n", "invalid line number: 0", filename, 1, 12},
		{"//line foo:1 \n", "invalid line number: 1 ", filename, 1, 12},
		{"//line foo:-12\n", "invalid line number: -12", filename, 1, 12},
		{"//line C:foo:0\n", "invalid line number: 0", filename, 1, 14},
		{fmt.Sprintf("//line foo:%d\n", tooLarge), fmt.Sprintf("invalid line number: %d", tooLarge), filename, 1, 12},

		// invalid //line directives with two colons
		{"//line ::\n", "invalid line number: ", filename, 1, 10},
		{"//line ::x\n", "invalid line number: x", filename, 1, 10},
		{"//line foo::123abc\n", "invalid line number: 123abc", filename, 1, 13},
		{"//line foo::0\n", "invalid line number: 0", filename, 1, 13},
		{"//line foo:0:1\n", "invalid line number: 0", filename, 1, 12},

		{"//line :123:0\n", "invalid column number: 0", filename, 1, 13},
		{"//line foo:123:0\n", "invalid column number: 0", filename, 1, 16},
		{fmt.Sprintf("//line foo:10:%d\n", tooLarge), fmt.Sprintf("invalid column number: %d", tooLarge), filename, 1, 15},

		// effect of valid //line directives on lines
		{"//line foo:123\n   foo", valid, "foo", 123, 0},
		{"//line  foo:123\n   foo", valid, " foo", 123, 0},
		{"//line foo:123\n//line bar:345\nfoo", valid, "bar", 345, 0},
		{"//line C:foo:123\n", valid, "C:foo", 123, 0},
		{"//line /src/a/a.go:123\n   foo", valid, "/src/a/a.go", 123, 0},
		{"//line :x:1\n", valid, ":x", 1, 0},
		{"//line foo ::1\n", valid, "foo :", 1, 0},
		{"//line foo:123abc:1\n", valid, "foo:123abc", 1, 0},
		{"//line foo :123:1\n", valid, "foo ", 123, 1},
		{"//line ::123\n", valid, ":", 123, 0},

		// effect of valid //line directives on columns
		{"//line :x:1:10\n", valid, ":x", 1, 10},
		{"//line foo ::1:2\n", valid, "foo :", 1, 2},
		{"//line foo:123abc:1:1000\n", valid, "foo:123abc", 1, 1000},
		{"//line foo :123:1000\n\n", valid, "foo ", 124, 1},
		{"//line ::123:1234\n", valid, ":", 123, 1234},

		// //line directives with omitted filenames lead to empty filenames
		{"//line :10\n", valid, "", 10, 0},
		{"//line :10:20\n", valid, filename, 10, 20},
		{"//line bar:1\n//line :10\n", valid, "", 10, 0},
		{"//line bar:1\n//line :10:20\n", valid, "bar", 10, 20},

		// ignored /*line directives
		{"/**/", valid, filename, 1, 5},             // no directive
		{"/*line*/", valid, filename, 1, 9},         // missing colon
		{"/*line foo*/", valid, filename, 1, 13},    // missing colon
		{"  //line foo:*/", valid, filename, 1, 16}, // not a line start
		{"/*  line foo:*/", valid, filename, 1, 16}, // space between // and line

		// invalid /*line directives with one colon
		{"/*line :*/", "invalid line number: ", filename, 1, 9},
		{"/*line :x*/", "invalid line number: x", filename, 1, 9},
		{"/*line foo :*/", "invalid line number: ", filename, 1, 13},
		{"/*line foo:x*/", "invalid line number: x", filename, 1, 12},
		{"/*line foo:0*/", "invalid line number: 0", filename, 1, 12},
		{"/*line foo:1 */", "invalid line number: 1 ", filename, 1, 12},
		{"/*line C:foo:0*/", "invalid line number: 0", filename, 1, 14},
		{fmt.Sprintf("/*line foo:%d*/", tooLarge), fmt.Sprintf("invalid line number: %d", tooLarge), filename, 1, 12},

		// invalid /*line directives with two colons
		{"/*line ::*/", "invalid line number: ", filename, 1, 10},
		{"/*line ::x*/", "invalid line number: x", filename, 1, 10},
		{"/*line foo::123abc*/", "invalid line number: 123abc", filename, 1, 13},
		{"/*line foo::0*/", "invalid line number: 0", filename, 1, 13},
		{"/*line foo:0:1*/", "invalid line number: 0", filename, 1, 12},

		{"/*line :123:0*/", "invalid column number: 0", filename, 1, 13},
		{"/*line foo:123:0*/", "invalid column number: 0", filename, 1, 16},
		{fmt.Sprintf("/*line foo:10:%d*/", tooLarge), fmt.Sprintf("invalid column number: %d", tooLarge), filename, 1, 15},

		// effect of valid /*line directives on lines
		{"/*line foo:123*/   foo", valid, "foo", 123, 0},
		{"/*line foo:123*/\n//line bar:345\nfoo", valid, "bar", 345, 0},
		{"/*line C:foo:123*/", valid, "C:foo", 123, 0},
		{"/*line /src/a/a.go:123*/   foo", valid, "/src/a/a.go", 123, 0},
		{"/*line :x:1*/", valid, ":x", 1, 0},
		{"/*line foo ::1*/", valid, "foo :", 1, 0},
		{"/*line foo:123abc:1*/", valid, "foo:123abc", 1, 0},
		{"/*line foo :123:10*/", valid, "foo ", 123, 10},
		{"/*line ::123*/", valid, ":", 123, 0},

		// effect of valid /*line directives on columns
		{"/*line :x:1:10*/", valid, ":x", 1, 10},
		{"/*line foo ::1:2*/", valid, "foo :", 1, 2},
		{"/*line foo:123abc:1:1000*/", valid, "foo:123abc", 1, 1000},
		{"/*line foo :123:1000*/\n", valid, "foo ", 124, 1},
		{"/*line ::123:1234*/", valid, ":", 123, 1234},

		// /*line directives with omitted filenames lead to the previously used filenames
		{"/*line :10*/", valid, "", 10, 0},
		{"/*line :10:20*/", valid, filename, 10, 20},
		{"//line bar:1\n/*line :10*/", valid, "", 10, 0},
		{"//line bar:1\n/*line :10:20*/", valid, "bar", 10, 20},
	} {
		base := NewFileBase(filename)
		_, err := Parse(base, strings.NewReader(test.src), nil, nil, 0)
		if err == nil {
			t.Errorf("%s: no error reported", test.src)
			continue
		}
		perr, ok := err.(Error)
		if !ok {
			t.Errorf("%s: got %v; want parser error", test.src, err)
			continue
		}
		if msg := perr.Msg; msg != test.msg {
			t.Errorf("%s: got msg = %q; want %q", test.src, msg, test.msg)
		}

		pos := perr.Pos
		if filename := pos.RelFilename(); filename != test.filename {
			t.Errorf("%s: got filename = %q; want %q", test.src, filename, test.filename)
		}
		if line := pos.RelLine(); line != test.line {
			t.Errorf("%s: got line = %d; want %d", test.src, line, test.line)
		}
		if col := pos.RelCol(); col != test.col {
			t.Errorf("%s: got col = %d; want %d", test.src, col, test.col)
		}
	}
}

// Test that typical uses of UnpackListExpr don't allocate.
func TestUnpackListExprAllocs(t *testing.T) {
	var x Expr = NewName(Pos{}, "x")
	allocs := testing.AllocsPerRun(1000, func() {
		list := UnpackListExpr(x)
		if len(list) != 1 || list[0] != x {
			t.Fatalf("unexpected result")
		}
	})

	if allocs > 0 {
		errorf := t.Errorf
		if testenv.OptimizationOff() {
			errorf = t.Logf // noopt builder disables inlining
		}
		errorf("UnpackListExpr allocated %v times", allocs)
	}
}

/ Helper function to parse an expression string for testing
func parseTestExpr(t *testing.T, exprStr string) Expr {
	t.Helper()
	src := fmt.Sprintf("package p; var _ = %s", exprStr)
	fbase := NewFileBase("test.go")
	file, err := Parse(fbase, strings.NewReader(src), nil, nil, CheckBranches)
	if err != nil {
		t.Fatalf("Parse(%q) failed: %v", exprStr, err)
		return nil
	}

	if len(file.DeclList) != 1 {
		t.Fatalf("Parse(%q): expected 1 declaration, got %d", exprStr, len(file.DeclList))
		return nil
	}

	decl, ok := file.DeclList[0].(*VarDecl)
	if !ok {
		t.Fatalf("Parse(%q): expected VarDecl, got %T", exprStr, file.DeclList[0])
		return nil
	}

	if decl.Values == nil {
		t.Fatalf("Parse(%q): expected VarDecl with values, got nil", exprStr)
		return nil
	}
	return decl.Values // This should be the parsed expression
}

// Helper to create a string representation of an AST node for comparison
func astToString(n Node) string {
	if n == nil {
		return "<nil>"
	}
	// Using Fprint with default options for a somewhat stable string representation.
	// For more detailed or specific checks, direct field comparison or a custom stringifier might be needed.
	var buf bytes.Buffer
	Fprint(&buf, n, PrintingFlags(0)) // Default printing
	return buf.String()
}

func TestTernaryOperatorExpr(t *testing.T) {
	tests := []struct {
		name     string
		exprStr  string
		expected string // Expected string representation of the AST
		errMsg   string // Expected error message if parsing should fail
	}{
		{
			name:    "basic ternary",
			exprStr: "cond ? trueVal : falseVal",
			expected: "cond ? trueVal : falseVal", // Note: This is a simplified string, actual AST will be nested.
			// We will need a more robust way to check AST structure.
		},
		{
			name:    "condition is comparison",
			exprStr: "a > b ? t : f",
			expected: "a > b ? t : f",
		},
		{
			name:    "true/false are complex",
			exprStr: `c ? (x + y) : (z * p)`, // Parentheses added for clarity in expected string
			expected: "c ? x + y : z * p", // String() might strip some parens if not syntactically required by precedence
		},
		{
			name: "right associative",
			exprStr: "c1 ? t1 : c2 ? t2 : f2",
			expected: "c1 ? t1 : c2 ? t2 : f2", // c1 ? t1 : (c2 ? t2 : f2)
		},
		{
			name: "precedence: OR vs ternary", // || has higher precedence than ? in our setup, so (a || b) ? t : f
			exprStr: "a || b ? t : f",
			expected: "a || b ? t : f",
		},
		{
			name: "precedence: ternary vs OR", // (c ? t : f) || x
			exprStr: "c ? t : f || x",
			expected: "(c ? t : f) || x",
		},
		{
			name: "precedence: AND vs ternary", // (a && b) ? t : f
			exprStr: "a && b ? t : f",
			expected: "a && b ? t : f",
		},
		{
			name: "precedence: ternary vs AND", // (c ? t : f) && x
			exprStr: "c ? t : f && x",
			expected: "(c ? t : f) && x",
		},
		{
			name: "precedence: ADD vs ternary", // (a + b) ? t : f
			exprStr: "a + b ? t : f",
			expected: "a + b ? t : f",
		},
		{
			name: "precedence: ternary vs ADD", // c ? t : (f + x)
			exprStr: "c ? t : f + x",
			expected: "c ? t : f + x",
		},
		{
			name:     "error: missing colon",
			exprStr:  "cond ? trueVal falseVal",
			errMsg:   "expected ':' in ternary expression",
		},
		{
			name:     "error: missing false branch value",
			exprStr:  "cond ? trueVal :",
			// The error here might be "unexpected EOF" or "expected expression"
			// depending on how the parser handles it after ':'.
			// Let's assume it expects an operand.
			errMsg:   "expected expression",
		},
		{
			name:     "error: missing true branch value",
			exprStr:  "cond ? : falseVal",
			errMsg:   "expected expression", // Error occurs when parsing true branch
		},
		// TODO: Add more tests, especially for nested structures and complex interactions.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We need a way to catch expected parsing errors.
			// The current Parse function calls t.Fatalf on error.
			// We might need to adjust parseTestExpr or use Parse directly with a custom error handler.

			var errs []Error
			handler := func(err Error) {
				errs = append(errs, err)
			}

			src := fmt.Sprintf("package p; var _ = %s", tt.exprStr)
			fbase := NewFileBase("test.go")
			file, _ := Parse(fbase, strings.NewReader(src), handler, nil, CheckBranches)

			if tt.errMsg != "" {
				if len(errs) == 0 {
					t.Errorf("expected error %q, got none", tt.errMsg)
				} else {
					found := false
					for _, err := range errs {
						if strings.Contains(err.Msg, tt.errMsg) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error msg containing %q, got: %v", tt.errMsg, errs)
					}
				}
				return // Don't check AST if error was expected
			}

			if len(errs) > 0 {
				t.Fatalf("Parse(%q) failed: %v", tt.exprStr, errs)
				return
			}

			if file == nil || len(file.DeclList) != 1 {
				t.Fatalf("Parse(%q): expected 1 declaration, got %d (file: %v)", tt.exprStr, len(file.DeclList), file)
				return
			}
			decl, ok := file.DeclList[0].(*VarDecl)
			if !ok || decl.Values == nil {
				t.Fatalf("Parse(%q): expected VarDecl with values, got %T", tt.exprStr, file.DeclList[0])
				return
			}

			parsedExpr := decl.Values

			// For now, we use a simplified string comparison.
			// A more robust check would involve traversing the AST.
			// The default String() for TernaryExpr is not yet defined, so this will likely fail or be unhelpful.
			// We need to implement String() for TernaryExpr or use a structural comparison.

			// Placeholder for actual AST structural check or more detailed string check
			// Let's try to print the parsed expression and see its default string form
			actualStr := astToString(parsedExpr)
			t.Logf("Parsed AST for %q:\n%s", tt.exprStr, actualStr)


			// This is a very basic check. The `expected` string needs to be carefully crafted
			// to match how `syntax.String()` (or `astToString`) formats the AST.
			// For TernaryExpr, we haven't defined a String() method yet, so it will use a default.
			// Let's refine this part once we see the output or implement a String() method.
			// For now, this test will likely require manual inspection of the logged AST for correctness.

			// A temporary, simplified check. This will need significant improvement.
			if tt.expected != "" {
				// This is a placeholder. Actual robust checking of AST structure is needed.
				// For example, checking node types and specific fields.
				// e.g., if parsedExpr.(*TernaryExpr).Cond is what we expect.
				// For now, let's just ensure it parses without error if no errMsg is set.
				if parsedExpr == nil {
					t.Errorf("Parsed expression is nil for %q", tt.exprStr)
				}
				// A more concrete check will be added iteratively.
				// For instance, checking the root node type:
				switch tt.name {
				case "basic ternary", "condition is comparison", "true/false are complex", "right associative",
					"precedence: OR vs ternary", "precedence: AND vs ternary", "precedence: ADD vs ternary":
					if _, ok := parsedExpr.(*TernaryExpr); !ok && tt.errMsg == "" {
						// If the root is not directly a ternary, it might be due to parentheses stripping.
						// e.g. `(a?b:c)` might parse to TernaryExpr, `a?b:c` also to TernaryExpr.
						// If it's `(a?b:c) || x`, then root is `||` Op.
						if !strings.Contains(tt.exprStr, "||") && !strings.Contains(tt.exprStr, "&&") && !strings.Contains(tt.exprStr, "+") {
							// Allow for ParenExpr wrapper if the original expression was parenthesized
							if pe, isParen := parsedExpr.(*ParenExpr); isParen {
								if _, ok := pe.X.(*TernaryExpr); !ok {
									t.Errorf("[%s] Expected root to be TernaryExpr or ParenExpr(TernaryExpr), got %T", tt.name, parsedExpr)
								}
							} else if _, ok := parsedExpr.(*TernaryExpr); !ok {
								t.Errorf("[%s] Expected root to be TernaryExpr, got %T", tt.name, parsedExpr)
							}
						}
					}
				case "precedence: ternary vs OR":
					op, ok := parsedExpr.(*Operation)
					if !ok || op.Op != OrOr {
						t.Errorf("[%s] Expected root to be OrOr Operation, got %T (Op: %v)", tt.name, parsedExpr, op.Op)
					}
				case "precedence: ternary vs AND":
					op, ok := parsedExpr.(*Operation)
					if !ok || op.Op != AndAnd {
						t.Errorf("[%s] Expected root to be AndAnd Operation, got %T (Op: %v)", tt.name, parsedExpr, op.Op)
					}
				case "precedence: ternary vs ADD":
					op, ok := parsedExpr.(*Operation)
					if !ok || op.Op != Add { // Assuming + is Add
						t.Errorf("[%s] Expected root to be Add Operation, got %T (Op: %v)", tt.name, parsedExpr, op.Op)
					}
				}
			}
		})
	}
}