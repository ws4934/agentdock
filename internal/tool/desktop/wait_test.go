package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func textPointer(s string) *string { return &s }
func msPointer(n int) *int         { return &n }
func TestWaitPredicatesNeverDispatchOrReplaceSnapshots(t *testing.T) {
	for _, condition := range []string{"window_exists", "window_stable", "element_exists", "element_enabled", "element_disabled", "element_absent", "window_absent"} {
		t.Run(condition, func(t *testing.T) {
			b := windowFixture()
			s := New(true, b)
			defer s.Close()
			id := observeWindow(t, s)
			r := WaitRequest{PID: 20, WindowID: 9, Condition: condition, TimeoutMS: msPointer(400), PollMS: 100, StableMS: 100}
			if strings.HasPrefix(condition, "element_") {
				r.Element = &ElementSelector{Role: "AXButton", Title: textPointer("Save")}
			}
			if condition == "element_disabled" {
				b.tree.Elements[0].Enabled = false
			}
			if condition == "element_absent" {
				r.Element.Title = textPointer("not present")
			}
			if condition == "window_absent" {
				r.WindowID = 99
			}
			result, err := s.Wait(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if result["met"] != true || result["input_dispatched"] != false || result["application_verified"] != false {
				t.Fatal(result)
			}
			if len(b.inputs)+len(b.events) != 0 {
				t.Fatal("wait sent input")
			}
			if s.latest == nil || s.latest.id != id {
				t.Fatal("wait replaced the action snapshot")
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), `"path"`) || result["snapshot_id"] != nil {
				t.Fatal("wait leaked AX path or authorized input")
			}
		})
	}
}
func TestWaitAbsenceAndAmbiguityFailClosed(t *testing.T) {
	cases := []string{"truncated_tree", "ambiguous_elements", "truncated_windows", "missing_window", "renamed_absent", "wrong_owner", "enabled_unknown"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			b := windowFixture()
			s := New(true, b)
			defer s.Close()
			r := WaitRequest{PID: 20, WindowID: 9, Condition: "element_absent", Element: &ElementSelector{Title: textPointer("missing")}, TimeoutMS: msPointer(0)}
			switch c {
			case "truncated_tree":
				b.tree.Truncated = true
			case "ambiguous_elements":
				r.Condition = "element_exists"
				r.Element = &ElementSelector{Role: "AXButton"}
				b.tree.Elements = append(b.tree.Elements, b.tree.Elements[0])
			case "truncated_windows":
				r.Element = nil
				r.Condition = "window_absent"
				r.WindowID = 999
				b.state.WindowsTruncated = true
			case "missing_window":
				r.WindowID = 99
			case "renamed_absent":
				r.Element = nil
				r.Condition = "window_absent"
				r.WindowTitle = textPointer("other")
			case "enabled_unknown":
				r.Condition = "element_disabled"
				r.Element = &ElementSelector{Role: "AXButton"}
				b.tree.Elements[0].Enabled = false
				b.tree.Elements[0].EnabledKnown = false
			case "wrong_owner":
				r.PID = 10
			}
			result, err := s.Wait(t.Context(), r)
			if c == "wrong_owner" {
				requireCode(t, err, "STALE_SNAPSHOT")
				return
			}
			if c == "renamed_absent" {
				requireCode(t, err, "INVALID_ARGUMENT")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result["met"] != false || result["timed_out"] != true {
				t.Fatal("uncertain condition was accepted", result)
			}
		})
	}
}
func TestWaitCancellationAndLimits(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	for _, r := range []WaitRequest{{}, {PID: 20, Condition: "bad"}, {PID: 20, Condition: "window_absent"}, {PID: 20, Condition: "element_exists", WindowID: 9}, {PID: 20, Condition: "window_exists", PollMS: 1}, {PID: 20, Condition: "window_exists", TimeoutMS: msPointer(30001)}} {
		_, err := s.Wait(t.Context(), r)
		requireCode(t, err, "INVALID_ARGUMENT")
	}
	ctx, cancel := context.WithCancel(t.Context())
	timer := time.AfterFunc(40*time.Millisecond, cancel)
	defer timer.Stop()
	_, err := s.Wait(ctx, WaitRequest{PID: 20, WindowID: 99, Condition: "window_exists", TimeoutMS: msPointer(30000)})
	if err == nil {
		t.Fatal("cancelled wait returned success")
	}
	if s.control.status().Phase != "paused" {
		t.Fatal("cancelled wait did not stop the control session")
	}
}
