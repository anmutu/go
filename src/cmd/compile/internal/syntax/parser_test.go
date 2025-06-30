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

// Helper function to parse an expression string for testing
func parseTestExpr(t *testing.T, exprStr string) (expr Expr, errors []Error) {
	t.Helper()
	src := fmt.Sprintf("package p; var _ = %s", exprStr)
	// fbase is important for error reporting if positions matter for your error assertions.
	// If NewFileBase is problematic or not needed for error message string comparison, this can be simplified.
	fbase := NewFileBase("test.go")

	var collectedSyntaxErrors []Error

	errh := func(err error) { // Corrected signature: func(err error)
		if err == nil {
			return
		}
		// Attempt to get specific syntax.Error information if possible
		if syntaxErr, ok := err.(Error); ok { // Correct type assertion to value type syntax.Error
			collectedSyntaxErrors = append(collectedSyntaxErrors, syntaxErr)
		} else {
			// Fallback for generic errors, or log them if unexpected
			// t.Logf("parseTestExpr handler received a non-syntax.Error: %T %v", err, err) // 可选的日志输出
			collectedSyntaxErrors = append(collectedSyntaxErrors, Error{Msg: err.Error()}) // Pos 会是零值
		}
	}

	file, firstErr := Parse(fbase, strings.NewReader(src), errh, nil, CheckBranches)

	if firstErr != nil && len(collectedSyntaxErrors) == 0 {
		if syntaxErr, ok := firstErr.(Error); ok {
			collectedSyntaxErrors = append(collectedSyntaxErrors, syntaxErr)
		} else {
			collectedSyntaxErrors = append(collectedSyntaxErrors, Error{Msg: firstErr.Error()})
		}
	}

	// 检查文件是否成功解析且包含预期的声明结构
	if file == nil || len(file.DeclList) == 0 {
		if len(collectedSyntaxErrors) == 0 {
			errMsg := "Parse produced no declarations"
			if firstErr != nil {
				errMsg = fmt.Sprintf("Parse produced no declarations (firstErr: %v)", firstErr)
			}
			collectedSyntaxErrors = append(collectedSyntaxErrors, Error{Msg: errMsg})
		}
		// 即使 decl.Values 可能是 nil，我们也返回收集到的错误
		// 如果 decl.Values 是 nil，调用者（测试用例）应该检查它并据此失败
		var varDeclValues Expr // Default to nil
		if file != nil && len(file.DeclList) > 0 {
			if decl, ok := file.DeclList[0].(*VarDecl); ok {
				varDeclValues = decl.Values
			}
		}
		return varDeclValues, collectedSyntaxErrors
	}

	decl, ok := file.DeclList[0].(*VarDecl)
	if !ok {
		if len(collectedSyntaxErrors) == 0 {
			errMsg := fmt.Sprintf("Parse expected VarDecl, got %T", file.DeclList[0])
			if firstErr != nil {
				errMsg = fmt.Sprintf("Parse expected VarDecl, got %T (firstErr: %v)", file.DeclList[0], firstErr)
			}
			collectedSyntaxErrors = append(collectedSyntaxErrors, Error{Msg: errMsg})
		}
		return nil, collectedSyntaxErrors
	}

	return decl.Values, collectedSyntaxErrors
}

// Helper to create a string representation of an AST node for comparison
func astToString(n Node) string {
	if n == nil {
		return "<nil>"
	}
	var buf bytes.Buffer
	Fprint(&buf, n, 0) // 使用整数 0 代表默认打印模式
	return buf.String()
}

func TestTernaryOperatorExpr(t *testing.T) {
	tests := []struct {
		name        string
		exprStr     string
		expectedAST func(t *testing.T, expr Expr) // 用于细致的AST结构检查 (可选)
		errMsg      string                        // 期望的错误信息中包含的子串 (如果errMsg非空，则期望有错误)
		// noError     bool   // 通过errMsg是否为空来隐式判断是否期望没有错误
	}{
		{
			name:    "basic ternary",
			exprStr: "cond ? trueVal : falseVal",
			expectedAST: func(t *testing.T, expr Expr) {
				if expr == nil {
					t.Errorf("FAIL: Test %q: parsed expression is nil", t.Name())
					return
				}
				tern, ok := Unparen(expr).(*TernaryExpr) // Unparen 以处理可能的顶层括号
				if !ok {
					t.Errorf("FAIL: Test %q: expected TernaryExpr, got %T (%s)", t.Name(), Unparen(expr), astToString(Unparen(expr)))
					return
				}
				if condName, ok := Unparen(tern.Cond).(*Name); !ok || condName.Value != "cond" {
					t.Errorf("FAIL: Test %q: expected Cond to be Name 'cond', got %s", t.Name(), astToString(tern.Cond))
				}
				if trueName, ok := Unparen(tern.True).(*Name); !ok || trueName.Value != "trueVal" {
					t.Errorf("FAIL: Test %q: expected True to be Name 'trueVal', got %s", t.Name(), astToString(tern.True))
				}
				if falseName, ok := Unparen(tern.False).(*Name); !ok || falseName.Value != "falseVal" {
					t.Errorf("FAIL: Test %q: expected False to be Name 'falseVal', got %s", t.Name(), astToString(tern.False))
				}
			},
		},
		{
			name:    "condition is comparison",
			exprStr: "a > b ? t : f",
			expectedAST: func(t *testing.T, expr Expr) {
				if expr == nil {
					t.Errorf("FAIL: Test %q: parsed expression is nil", t.Name())
					return
				}
				tern, ok := Unparen(expr).(*TernaryExpr)
				if !ok {
					t.Errorf("FAIL: Test %q: expected TernaryExpr, got %T (%s)", t.Name(), Unparen(expr), astToString(Unparen(expr)))
					return
				}
				op, ok := Unparen(tern.Cond).(*Operation)
				if !ok || op.Op != Gtr {
					t.Errorf("FAIL: Test %q: expected Cond to be Gtr Operation, got %s", t.Name(), astToString(tern.Cond))
				}
			},
		},
		{
			name:    "true/false are complex",
			exprStr: `c ? (x + y) : (z * p)`,
		},
		{
			name:    "right associative",
			exprStr: "c1 ? t1 : c2 ? t2 : f2", // c1 ? t1 : (c2 ? t2 : f2)
			expectedAST: func(t *testing.T, expr Expr) {
				if expr == nil {
					t.Errorf("FAIL: Test %q: parsed expression is nil", t.Name())
					return
				}
				tern1, ok := Unparen(expr).(*TernaryExpr)
				if !ok {
					t.Errorf("FAIL: Test %q: expected outer TernaryExpr, got %T (%s)", t.Name(), Unparen(expr), astToString(Unparen(expr)))
					return
				}
				if _, okInner := Unparen(tern1.False).(*TernaryExpr); !okInner {
					t.Errorf("FAIL: Test %q: expected False branch to be a TernaryExpr for right-associativity, got %s", t.Name(), astToString(tern1.False))
				}
			},
		},
		{
			name:    "precedence: OR vs ternary", // (a || b) ? t : f
			exprStr: "a || b ? t : f",
			expectedAST: func(t *testing.T, expr Expr) {
				if expr == nil {
					t.Errorf("FAIL: Test %q: parsed expression is nil", t.Name())
					return
				}
				tern, ok := Unparen(expr).(*TernaryExpr)
				if !ok {
					t.Errorf("FAIL: Test %q: expected TernaryExpr, got %T (%s)", t.Name(), Unparen(expr), astToString(Unparen(expr)))
					return
				}
				op, ok := Unparen(tern.Cond).(*Operation)
				if !ok || op.Op != OrOr {
					t.Errorf("FAIL: Test %q: expected Cond to be OrOr Operation, got %s", t.Name(), astToString(tern.Cond))
				}
			},
		},
		{
			name:    "precedence: ternary vs OR", // 我们实际得到的是 c ? t : (f || x)
			exprStr: "c ? t : f || x",
			expectedAST: func(t *testing.T, expr Expr) {
				if expr == nil {
					t.Errorf("FAIL: Test %q: parsed expression is nil", t.Name())
					return
				}
				tern, ok := Unparen(expr).(*TernaryExpr) // 期望根节点是 TernaryExpr
				if !ok {
					t.Fatalf("FAIL: Test %q: expected root to be TernaryExpr, got %T (%s)", t.Name(), Unparen(expr), astToString(Unparen(expr)))
				}
				// 检查 Cond 和 True 是否符合预期 (例如是简单的 Name)
				if nameC, ok := Unparen(tern.Cond).(*Name); !ok || nameC.Value != "c" {
					t.Errorf("FAIL: Test %q: expected Cond to be Name 'c', got %s", t.Name(), astToString(tern.Cond))
				}
				if nameT, ok := Unparen(tern.True).(*Name); !ok || nameT.Value != "t" {
					t.Errorf("FAIL: Test %q: expected True to be Name 't', got %s", t.Name(), astToString(tern.True))
				}
				// 关键：检查 False 分支是否是一个 || 操作
				op, ok := Unparen(tern.False).(*Operation)
				if !ok || op.Op != OrOr {
					t.Errorf("FAIL: Test %q: expected False branch to be OrOr Operation, got %T (%s)", t.Name(), Unparen(tern.False), astToString(Unparen(tern.False)))
				}
				// （可选）进一步检查 op.X (应该是 "f") 和 op.Y (应该是 "x")
				if nameF, ok := Unparen(op.X).(*Name); !ok || nameF.Value != "f" {
					t.Errorf("FAIL: Test %q: expected False branch's left operand (X of ||) to be Name 'f', got %s", t.Name(), astToString(op.X))
				}
				if nameX, ok := Unparen(op.Y).(*Name); !ok || nameX.Value != "x" {
					t.Errorf("FAIL: Test %q: expected False branch's right operand (Y of ||) to be Name 'x', got %s", t.Name(), astToString(op.Y))
				}
			},
		},
		{name: "precedence: AND vs ternary", exprStr: "a && b ? t : f"},
		{name: "precedence: ternary vs AND", exprStr: "c ? t : f && x"},
		{name: "precedence: ADD vs ternary", exprStr: "a + b ? t : f"},
		{name: "precedence: ternary vs ADD", exprStr: "c ? t : f + x"},
		{
			name:    "error: missing colon",
			exprStr: "cond ? trueVal falseVal",
			errMsg:  "expected ':' in ternary expression",
		},
		{
			name:    "error: missing false branch value",
			exprStr: "cond ? trueVal :",
			errMsg:  "expected expression",
		},
		{
			name:    "error: missing true branch value",
			exprStr: "cond ? : falseVal",
			errMsg:  "expected expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedExpr, errs := parseTestExpr(t, tt.exprStr)

			if tt.errMsg != "" { // Expect an error
				if len(errs) == 0 {
					t.Errorf("FAIL: Test %q: expected error containing %q, got no errors. Parsed AST: %s", tt.name, tt.errMsg, astToString(parsedExpr))
				} else {
					found := false
					for _, err := range errs {
						if strings.Contains(err.Msg, tt.errMsg) { // Check our collected Error's Msg field
							found = true
							break
						}
					}
					if !found {
						var msgs []string
						for _, err := range errs {
							msgs = append(msgs, err.Error()) // Use Error() for string representation
						}
						t.Errorf("FAIL: Test %q: expected error msg containing %q, got: [%s]", tt.name, tt.errMsg, strings.Join(msgs, "; "))
					}
				}
			} else { // Expect no error (noError is implied if errMsg is empty)
				if len(errs) > 0 {
					var msgs []string
					for _, err := range errs {
						msgs = append(msgs, err.Error())
					}
					t.Errorf("FAIL: Test %q: expected no error, but got: [%s]", tt.name, strings.Join(msgs, "; "))
				}
				if parsedExpr == nil && len(errs) == 0 {
					// This case should ideally be caught by parseTestExpr if it returns nil expr without errors.
					// However, an explicit check here is a safeguard.
					t.Errorf("FAIL: Test %q: expected a parsed expression, but got nil with no errors reported by handler", tt.name)
				}
				// If no error is expected, and an AST check function is provided, run it.
				if tt.expectedAST != nil && parsedExpr != nil {
					tt.expectedAST(t, parsedExpr)
				}
				// Log for successful non-error cases if verbose, and if no AST check failed.
				// We check t.Failed() to see if any t.Error/t.Fatal was called by expectedAST.
				if len(errs) == 0 && parsedExpr != nil && !t.Failed() {
					t.Logf("PASS: Test %q. Parsed AST for %q:\n%s", tt.name, tt.exprStr, astToString(parsedExpr))
				}
			}
		})
	}
}
