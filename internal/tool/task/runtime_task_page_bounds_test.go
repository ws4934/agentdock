package task

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeTaskPageFitsNativeResponseBudget(t *testing.T) {
	service, _ := newTaskTestService(t)
	// 大目标文本合法，但不能让列表超过原生客户端的固定响应边界。
	for i := 0; i < 40; i++ {
		if _, err := service.tasks.Create("fixture", strings.Repeat("x", 16<<10), []string{"verified"}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.RuntimeTasks("", 200)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 512*1024 || page["partial"] != true || page["count"].(int) == 0 {
		t.Fatalf("unbounded or silently incomplete page: %d bytes, count=%v partial=%v", len(data), page["count"], page["partial"])
	}
}
