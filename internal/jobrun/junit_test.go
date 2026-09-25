package jobrun

import (
	"github.com/uvwt/agentdock/internal/sourceproof"
	"os"
	"path/filepath"
	"testing"
)

func TestJUnitSummaryConsistency(t *testing.T) {
	for name, report := range map[string]string{
		"failure":     `<testsuite tests="1" failures="1"><testcase/></testsuite>`,
		"error":       `<testsuite tests="1" errors="1"><testcase/></testsuite>`,
		"skip":        `<testsuite tests="1" skipped="1"><testcase/></testsuite>`,
		"root":        `<testsuites tests="99"><testsuite tests="1"><testcase/></testsuite></testsuites>`,
		"roots":       `<testsuite tests="1"><testcase/></testsuite><testsuite/>`,
		"negative":    `<testsuite failures="-1"><testcase/></testsuite>`,
		"duplicate":   `<testsuite tests="1" tests="1"><testcase/></testsuite>`,
		"mixed":       `<testsuite><testcase><skipped/><failure/></testcase></testsuite>`,
		"nested_case": `<testsuite><properties><testcase/></properties></testsuite>`,
		"truncated":   `<testsuite tests="1"><testcase/>`,
		"doctype":     `<!DOCTYPE x><testsuite><testcase/></testsuite>`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.xml")
			if err := os.WriteFile(path, []byte(report), 0600); err != nil {
				t.Fatal(err)
			}
			obs := readJUnit(path)
			snap := sourceproof.Snapshot{Root: "fixture", Complete: true, Revision: "stable"}
			e := finishEvidence(Spec{Validation: &ValidationSpec{Adapter: "junit"}}, 0, obs, snap, snap)
			if obs.Complete || e.Status == "passed" {
				t.Fatalf("contradiction accepted: %+v %+v", obs, e)
			}
		})
	}
}
func TestJUnitNestedReportsAndDistinctErrors(t *testing.T) {
	report := `<testsuites tests="4" failures="1" errors="1" skipped="1"><testsuite tests="4" failures="1" errors="1" skipped="1"><testsuite tests="2" failures="1"><testcase/><testcase><failure>failure</failure></testcase></testsuite><testcase><error>error</error></testcase><testcase><skipped/></testcase></testsuite></testsuites>`
	path := filepath.Join(t.TempDir(), "report.xml")
	if err := os.WriteFile(path, []byte(report), 0600); err != nil {
		t.Fatal(err)
	}
	obs := readJUnit(path)
	if !obs.Complete || obs.Tests != 4 || obs.Passed != 1 || obs.Failed != 2 || obs.Skipped != 1 {
		t.Fatalf("%+v", obs)
	}
}
