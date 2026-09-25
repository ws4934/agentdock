package jobrun

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strconv"
)

// 逐层校验声明与实际结果，不把矛盾、截断或多根 XML 当作通过证据。
type junitCounts struct{ tests, failures, errors, skipped int }
type junitSuite struct {
	depth    int
	before   junitCounts
	declared map[string]int
}

func (c junitCounts) value(key string) int {
	switch key {
	case "tests":
		return c.tests
	case "failures":
		return c.failures
	case "errors":
		return c.errors
	case "skipped":
		return c.skipped
	}
	return 0
}
func junitDeclarations(attrs []xml.Attr) (map[string]int, bool) {
	result := map[string]int{}
	for _, a := range attrs {
		switch a.Name.Local {
		case "tests", "failures", "errors", "skipped":
		default:
			continue
		}
		if _, exists := result[a.Name.Local]; exists {
			return nil, false
		}
		n, err := strconv.Atoi(a.Value)
		if err != nil || n < 0 || n > 100000 || a.Name.Space != "" {
			return nil, false
		}
		result[a.Name.Local] = n
	}
	return result, true
}
func readJUnit(path string) testObservation {
	r := testObservation{}
	if regular(path) != nil {
		return r
	}
	f, err := os.Open(path)
	if err != nil {
		return r
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > 4<<20 {
		return r
	}
	limited := &io.LimitedReader{R: f, N: (4 << 20) + 1}
	decoder := xml.NewDecoder(limited)
	stack := []string{}
	suites := []junitSuite{}
	counts := junitCounts{}
	roots, caseDepth := 0, 0
	outcome := ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return r
		}
		switch v := token.(type) {
		case xml.Directive:
			return r
		case xml.CharData:
			if len(stack) == 0 && len(bytes.TrimSpace(v)) > 0 {
				return r
			}
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			} else {
				roots++
				if roots != 1 || (v.Name.Local != "testsuite" && v.Name.Local != "testsuites") {
					return r
				}
			}
			stack = append(stack, v.Name.Local)
			if len(stack) > 64 {
				return r
			}
			switch v.Name.Local {
			case "testsuite", "testsuites":
				if parent != "" && parent != "testsuite" && parent != "testsuites" {
					return r
				}
				declared, ok := junitDeclarations(v.Attr)
				if !ok {
					return r
				}
				suites = append(suites, junitSuite{len(stack), counts, declared})
			case "testcase":
				if parent != "testsuite" || caseDepth != 0 {
					return r
				}
				caseDepth = len(stack)
				outcome = ""
			case "failure", "error", "skipped":
				if parent != "testcase" || caseDepth == 0 || outcome != "" {
					return r
				}
				outcome = v.Name.Local
			}
		case xml.EndElement:
			depth := len(stack)
			if depth == 0 {
				return r
			}
			if v.Name.Local == "testcase" && depth == caseDepth {
				counts.tests++
				r.Tests++
				if counts.tests > 100000 {
					return r
				}
				switch outcome {
				case "failure":
					counts.failures++
					r.Failed++
				case "error":
					counts.errors++
					r.Failed++
				case "skipped":
					counts.skipped++
					r.Skipped++
				default:
					r.Passed++
				}
				caseDepth = 0
			}
			if v.Name.Local == "testsuite" || v.Name.Local == "testsuites" {
				if len(suites) == 0 {
					return r
				}
				suite := suites[len(suites)-1]
				if suite.depth != depth {
					return r
				}
				for key, n := range suite.declared {
					if counts.value(key)-suite.before.value(key) != n {
						return r
					}
				}
				suites = suites[:len(suites)-1]
			}
			stack = stack[:depth-1]
		}
	}
	after, err := f.Stat()
	if err != nil || limited.N <= 0 || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return r
	}
	r.Complete = roots == 1 && len(stack) == 0 && len(suites) == 0 && caseDepth == 0
	return r
}
