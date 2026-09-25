package jobrun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyMissingIdentityRequiresObservedBootTransition(t *testing.T) {
	if bootID() == "" {
		t.Skip("OS boot identity unavailable")
	}
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing_record_", missing), func(t *testing.T) {
			s := newTestStore(t)
			id := "job_00000000000000000000000000000019"
			dir := filepath.Join(s.Root, id)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if !missing {
				old := time.Now().Add(-time.Hour)
				r := Record{SchemaVersion: SchemaVersion, ID: id, CreatedAt: old, StartedAt: &old, Status: "running"}
				if err := writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
					t.Fatal(err)
				}
			}
			// 同一启动代际内，重复 abandon 也不能自证未知进程已退出。
			for i := 0; i < 2; i++ {
				if _, err := s.Abandon(t.Context(), id); err == nil || !strings.Contains(err.Error(), "PROCESS_EXIT_UNPROVEN") {
					t.Fatalf("unknown execution released: %v", err)
				}
			}
			path := filepath.Join(dir, "recovery-observation.json")
			var observed map[string]any
			if err := readJSON(path, &observed); err != nil {
				t.Fatal(err)
			}
			if observed["boot_id"] != bootID() || observed["job_id"] != id {
				t.Fatalf("%+v", observed)
			}
			// 仅在测试目录模拟先前保存的代际，不对用户系统执行重启。
			observed["boot_id"] = "fixture-previous-boot"
			if err := writeJSON(path, observed); err != nil {
				t.Fatal(err)
			}
			released, err := s.Abandon(t.Context(), id)
			if err != nil || released.Status != "abandoned" || released.ResolutionBasis != "recovery_observation_before_boot" || released.ExitCode != nil || released.Evidence != nil {
				t.Fatalf("%+v %v", released, err)
			}
			if _, err := s.Archive(t.Context(), id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
