package jobrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/uvwt/agentdock/internal/sourceproof"
)

func validateAdapter(spec Spec) error {
	switch spec.Validation.Adapter {
	case "go_test":
		if len(spec.Argv) < 4 || strings.TrimSuffix(filepath.Base(spec.Argv[0]), ".exe") != "go" || spec.Argv[1] != "test" || spec.Argv[2] != "-json" || spec.Argv[3] != "-count=1" {
			return errors.New("go_test requires go test -json -count=1")
		}
	case "junit":
		found := 0
		for _, a := range spec.Argv {
			found += strings.Count(a, "{report}")
		}
		if found != 1 || spec.Command != "" {
			return errors.New("junit requires argv with exactly one {report} placeholder")
		}
	case "process":
	default:
		return errors.New("adapter must be go_test, junit or process")
	}
	if len(spec.Validation.SourcePaths) > 128 {
		return errors.New("too many source paths")
	}
	for _, path := range spec.Validation.SourcePaths {
		if !filepath.IsLocal(path) || path == "." {
			return errors.New("source_paths must be explicit local paths")
		}
	}
	return nil
}

type testObservation struct {
	Tests, Passed, Failed, Skipped int
	Complete                       bool
}
type goCollector struct {
	mu       sync.Mutex
	pending  []byte
	invalid  bool
	tests    map[string]string
	packages map[string]string
}

func (g *goCollector) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		at := bytes.IndexByte(p, '\n')
		if at < 0 {
			if len(g.pending)+len(p) > 1<<20 {
				g.invalid = true
				g.pending = nil
			} else {
				g.pending = append(g.pending, p...)
			}
			break
		}
		if len(g.pending)+at > 1<<20 {
			g.invalid = true
			g.pending = nil
			p = p[at+1:]
			continue
		}
		line := append(g.pending, p[:at]...)
		g.pending = nil
		p = p[at+1:]
		var event struct{ Action, Package, Test string }
		if json.Unmarshal(line, &event) != nil || event.Package == "" {
			g.invalid = true
			continue
		}
		if g.tests == nil {
			g.tests = map[string]string{}
			g.packages = map[string]string{}
		}
		if len(g.tests) >= 100000 || len(g.packages) >= 20000 {
			g.invalid = true
			continue
		}
		if _, exists := g.packages[event.Package]; !exists {
			g.packages[event.Package] = "running"
		}
		switch event.Action {
		case "run":
			if event.Test != "" {
				g.tests[event.Package+"/"+event.Test] = "running"
			}
		case "pass", "fail", "skip":
			if event.Test != "" {
				g.tests[event.Package+"/"+event.Test] = event.Action
			} else {
				g.packages[event.Package] = event.Action
			}
		}
	}
	return n, nil
}
func (g *goCollector) result() testObservation {
	g.mu.Lock()
	defer g.mu.Unlock()
	r := testObservation{Complete: !g.invalid && len(bytes.TrimSpace(g.pending)) == 0 && len(g.packages) > 0}
	for _, state := range g.tests {
		r.Tests++
		switch state {
		case "pass":
			r.Passed++
		case "fail":
			r.Failed++
		case "skip":
			r.Skipped++
		default:
			r.Complete = false
		}
	}
	for _, state := range g.packages {
		if state != "pass" && state != "fail" && state != "skip" {
			r.Complete = false
		}
	}
	return r
}

func finishEvidence(spec Spec, code int, obs testObservation, before, after sourceproof.Snapshot) *Evidence {
	e := &Evidence{Adapter: spec.Validation.Adapter, Tests: obs.Tests, Passed: obs.Passed, Failed: obs.Failed, Skipped: obs.Skipped, ReportComplete: obs.Complete, SourceBefore: before, SourceAfter: after}
	switch {
	case code != 0 || obs.Failed > 0:
		e.Status = "failed"
		e.Reason = "execution_or_tests_failed"
	case !before.Complete || !after.Complete:
		e.Status = "inconclusive"
		e.Reason = "source_state_unproven"
	case !sourceproof.Matches(before, after):
		e.Status = "stale"
		e.Reason = "source_changed_during_validation"
	case spec.Validation.Adapter == "process":
		e.Status = "process_passed"
		e.Reason = "process_exit_is_not_test_execution_proof"
	case !obs.Complete:
		e.Status = "inconclusive"
		e.Reason = "test_report_incomplete_or_missing"
	case obs.Tests == 0 || obs.Passed+obs.Failed == 0:
		e.Status = "inconclusive"
		e.Reason = "zero_executed_tests"
	default:
		e.Status = "passed"
	}
	return e
}
