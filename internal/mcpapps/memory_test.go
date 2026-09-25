//go:build mcpapps_browser

package mcpapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/chromedp/cdproto/heapprofiler"
	"github.com/chromedp/cdproto/memory"
	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Review-only synthetic host. No user Chrome profile, network credentials or MCP calls.
func TestFeedbackBrowserRetainedIframeMemory(t *testing.T) {
	html, _ := json.Marshal(HTML("work_result", ""))
	fixture, _ := json.Marshal(fixtures["work_result"])
	host := fmt.Sprintf(`<!doctype html><meta charset="utf-8"><div id="cards"></div><script>
 const html=%s, data=%s;
 window.addCards=n=>{for(let i=0;i<n;i++){const f=document.createElement('iframe');f.srcdoc=html;document.getElementById('cards').append(f)}};
 window.teardownCards=()=>{for(const f of document.querySelectorAll('iframe')) f.contentWindow.postMessage({jsonrpc:'2.0',id:800,method:'ui/resource-teardown',params:{}},'*')};
 window.dropCards=()=>document.getElementById('cards').replaceChildren();
 window.addEventListener('message', e=>{
  const f=Array.from(document.querySelectorAll('iframe')).find(f=>f.contentWindow===e.source);if(!f)return;
  const m=e.data;
  if(m.method==='ui/initialize')e.source.postMessage({jsonrpc:'2.0',id:m.id,result:{protocolVersion:'2026-01-26',hostInfo:{name:'review-host',version:'1'},hostCapabilities:{},hostContext:{locale:'en',theme:'light'}}},'*');
  if(m.method==='ui/notifications/initialized')e.source.postMessage({jsonrpc:'2.0',method:'ui/notifications/tool-result',params:{structuredContent:data,content:[]}},'*');
 });
 </script>`, html, fixture)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, host)
	}))
	defer server.Close()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserBinary(t)), chromedp.UserDataDir(t.TempDir()), chromedp.Flag("disable-extensions", true), chromedp.Flag("disable-background-networking", true))
	alloc, stopAlloc := chromedp.NewExecAllocator(t.Context(), opts...)
	defer stopAlloc()
	browser, stopBrowser := chromedp.NewContext(alloc)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 150*time.Second)
	defer cancel()
	defer closeIsolatedBrowser(t, browser)
	run := func(a ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, a...); err != nil {
			t.Fatal(err)
		}
	}
	eval := func(s string) { t.Helper(); run(chromedp.Evaluate(s, nil)) }
	sample := func(label string, count int) float64 {
		t.Helper()
		var used, total, embedder, backing float64
		var docs, nodes, listeners int64
		run(chromedp.Sleep(250*time.Millisecond), chromedp.ActionFunc(func(ctx context.Context) error {
			if err := heapprofiler.CollectGarbage().Do(ctx); err != nil {
				return err
			}
			var err error
			used, total, embedder, backing, err = cdruntime.GetHeapUsage().Do(ctx)
			if err != nil {
				return err
			}
			docs, nodes, listeners, err = memory.GetDOMCounters().Do(ctx)
			return err
		}))
		t.Logf("FEEDBACK_MEMORY label=%s cards=%d used_mib=%.3f allocated_mib=%.3f embedder_mib=%.3f backing_mib=%.3f documents=%d nodes=%d listeners=%d", label, count, used/(1<<20), total/(1<<20), embedder/(1<<20), backing/(1<<20), docs, nodes, listeners)
		return used
	}
	run(chromedp.Navigate(server.URL))
	baseline := sample("baseline", 0)
	live := float64(0)
	previous := 0
	for _, count := range []int{1, 10, 30, 60, 100} {
		eval(fmt.Sprintf("addCards(%d)", count-previous))
		run(chromedp.Poll(fmt.Sprintf("document.querySelectorAll('iframe').length===%d && Array.from(document.querySelectorAll('iframe')).every(f=>f.contentDocument?.querySelector('[data-entity]'))", count), nil, chromedp.WithPollingTimeout(30*time.Second)))
		live = sample("retained", count)
		previous = count
	}
	eval("teardownCards()")
	run(chromedp.Poll("Array.from(document.querySelectorAll('iframe')).every(f=>f.contentDocument?.URL==='about:blank')", nil, chromedp.WithPollingTimeout(10*time.Second)))
	released := sample("teardown_iframes_retained", 100)
	if released > live*0.25 {
		t.Errorf("retained iframe teardown did not release SDK realm: live=%.1fMiB after=%.1fMiB", live/(1<<20), released/(1<<20))
	}
	eval("dropCards()")
	sample("removed", 0)
	for cycle := 1; cycle <= 3; cycle++ {
		eval("addCards(30)")
		run(chromedp.Poll("document.querySelectorAll('iframe').length===30 && Array.from(document.querySelectorAll('iframe')).every(f=>f.contentDocument?.querySelector('[data-entity]'))", nil, chromedp.WithPollingTimeout(30*time.Second)))
		eval("dropCards()")
		used := sample(fmt.Sprintf("removed_cycle_%d", cycle), 0)
		if used > baseline+(4<<20) {
			t.Errorf("detached realm retained after cycle %d: %.1fMiB", cycle, used/(1<<20))
		}
	}
}

func browserBinary(t *testing.T) string {
	t.Helper()
	if explicit := os.Getenv("AGENTDOCK_UI_CHROME"); explicit != "" {
		return explicit
	}
	if runtime.GOOS == "darwin" {
		return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Fatal("set AGENTDOCK_UI_CHROME to an installed isolated test browser")
	return ""
}
