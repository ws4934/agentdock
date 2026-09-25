//go:build mcpapps_browser

package mcpapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/heapprofiler"
	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// 独立合成宿主：后续进度只通过 task_read 请求返回，不给旧卡片推送新工具结果。
const liveProgressHost = `<!doctype html><meta charset="utf-8"><style>body{margin:0}iframe{display:block;width:600px;height:430px;border:0}</style><div id="cards"></div><script>
const html=%s;
window.framesList=[];window.reads=[];window.bad=[];window.mode='success';window.pending=null;
window.backend={id:'tsk_0123456789abcdef',title:'自动任务进度',summary:'初始进度',status:'active',step_count:2,completed_step_count:0,steps:[{id:'one',title:'检查',status:'in_progress'},{id:'two',title:'交付',status:'pending'}]};
window.sequence=0;
window.revision=()=> 'tsk1:'+sequence.toString(16).padStart(64,'0');
window.change=(summary,status='active')=>{backend.summary=summary;backend.status=status;sequence++;backend.completed_step_count=status==='completed'?2:1;backend.steps[0].status='completed';backend.steps[1].status=status==='completed'?'completed':'in_progress'};
window.send=(f,method,params)=>f.contentWindow.postMessage({jsonrpc:'2.0',method,params},'*');
window.addCards=n=>{for(let i=0;i<n;i++){const f=document.createElement('iframe');f.setAttribute('sandbox','allow-scripts allow-same-origin');f.srcdoc=html;document.getElementById('cards').append(f);framesList.push(f)}};
window.reply=(source,m)=>{
 if(mode==='deny'){source.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:'Denied by synthetic host'}},'*');return;}
 const rev=revision(), unchanged=m.params.arguments.if_revision===rev;
 const data={action:'snapshot',task_id:backend.id,revision:rev,unchanged};
 if(!unchanged)data.task_summary={...backend,revision:rev};
 if(mode==='wrong')data.task_id='tsk_ffffffffffffffff';
 source.postMessage({jsonrpc:'2.0',id:m.id,result:{structuredContent:data,content:[]}},'*');
};
window.teardown=()=>framesList.forEach(f=>f.contentWindow.postMessage({jsonrpc:'2.0',id:900,method:'ui/resource-teardown',params:{}},'*'));
window.addEventListener('message',e=>{
 const f=framesList.find(f=>f.contentWindow===e.source);if(!f)return;
 const m=e.data;
 if(m.method==='ui/initialize')e.source.postMessage({jsonrpc:'2.0',id:m.id,result:{protocolVersion:'2026-01-26',hostInfo:{name:'live-fixture',version:'1'},hostCapabilities:{serverTools:{},message:{text:{}}},hostContext:{theme:'light',locale:'zh-CN',availableDisplayModes:['inline','fullscreen','pip'],displayMode:'inline'}}},'*');
 if(m.method==='ui/notifications/initialized')send(f,'ui/notifications/tool-result',{structuredContent:{action:'create',task_id:backend.id,task_summary:{...backend,revision:revision()}},content:[]});
 if(m.method==='ui/notifications/size-changed'&&!f.dataset.floating&&!f.dataset.fixedSize)f.style.height=m.params.height+'px';
 if(m.method==='tools/call'){
  reads.push({frame:framesList.indexOf(f),name:m.params.name,args:m.params.arguments});
  if(m.params.name!=='task_read'||m.params.arguments.action!=='snapshot'||m.params.arguments.task_id!==backend.id){bad.push(m.params);return;}
  if(mode==='hold')pending={source:e.source,m};else reply(e.source,m);
 }
 if(m.method==='ui/request-display-mode'){
  const floating=m.params.mode!=='inline';f.dataset.floating=floating?'true':'';
  f.style.cssText=floating?'position:fixed;right:12px;bottom:12px;width:360px;height:360px;background:white':'display:block;width:600px;height:430px';
  e.source.postMessage({jsonrpc:'2.0',id:m.id,result:{mode:m.params.mode}},'*');
 }
});
addCards(1);
</script>`

func TestFeedbackBrowserLiveProgress(t *testing.T) {
	html := strings.Replace(HTML("task_progress", ""), "<script>", "<script>"+auditBootstrap+"</script><script>", 1)
	encoded, _ := json.Marshal(html)
	host := fmt.Sprintf(liveProgressHost, encoded)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, host)
	}))
	defer server.Close()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserBinary(t)), chromedp.UserDataDir(t.TempDir()), chromedp.Flag("disable-extensions", true), chromedp.Flag("disable-background-networking", true))
	alloc, stopAlloc := chromedp.NewExecAllocator(t.Context(), opts...)
	defer stopAlloc()
	browser, stopBrowser := chromedp.NewContext(alloc)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 110*time.Second)
	defer cancel()
	defer closeIsolatedBrowser(t, browser)
	run := func(actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			t.Fatal(err)
		}
	}
	eval := func(code string) any { t.Helper(); var value any; run(chromedp.Evaluate(code, &value)); return value }
	poll := func(code string) {
		t.Helper()
		run(chromedp.Poll(code, nil, chromedp.WithPollingTimeout(12*time.Second)))
	}
	doc := `framesList[0].contentDocument`
	check := func(code string) {
		t.Helper()
		if eval(code) != true {
			t.Fatalf("assertion %s; body=%v reads=%v", code, eval(doc+`.body.innerText`), eval(`reads`))
		}
	}
	run(chromedp.EmulateViewport(800, 900), chromedp.Navigate(server.URL))
	poll(doc + `?.querySelector('[data-live-state="live"]')!==null && reads.length===1`)
	check(`reads[0].args.if_revision===revision() && bad.length===0`)
	eval(doc + `.querySelector('.toggle').click();` + doc + `.querySelector('.toggle').focus();change('自动更新后的阶段')`)
	poll(doc + `.querySelector('.summary').textContent==='自动更新后的阶段'`)
	check(doc + `.querySelector('.toggle').getAttribute('aria-expanded')==='true' && ` + doc + `.activeElement===` + doc + `.querySelector('.toggle')`)
	check(`document.querySelectorAll('iframe').length===1 && reads.every(r=>r.name==='task_read')`)
	// 暂停不影响任务状态；恢复必须仍是只读查询。
	eval(doc + `.querySelector('.live-toggle').click();window.pauseReads=reads.length;change('暂停期间的新阶段')`)
	poll(doc + `.querySelector('[data-live-state="paused"]')!==null`)
	run(chromedp.Sleep(5200 * time.Millisecond))
	check(`reads.length===pauseReads`)
	eval(doc + `.querySelector('.live-toggle').click()`)
	poll(doc + `.querySelector('.summary').textContent==='暂停期间的新阶段'`)
	// 滚动出视口就停止；重新可见后获取当前版本。
	eval(`framesList[0].style.marginTop='3000px'`)
	poll(doc + `.querySelector('[data-live-state="hidden"]')!==null`)
	eval(`window.hiddenReads=reads.length;change('重新可见后更新')`)
	run(chromedp.Sleep(5200 * time.Millisecond))
	check(`reads.length===hiddenReads`)
	eval(`framesList[0].style.marginTop='0'`)
	poll(doc + `.querySelector('.summary').textContent==='重新可见后更新'`)
	// 受阻停更，保留手动刷新/跨会话入口；恢复后可重新启用自动更新。
	eval(`backend.blocker='需要人工决定';change('等待决定','blocked')`)
	poll(doc + `.querySelector('[data-live-state="stopped"]')!==null`)
	check(doc + `.querySelector('.summary').textContent==='需要人工决定'`)
	eval(`window.blockReads=reads.length;change('决策已记录','active');delete backend.blocker`)
	run(chromedp.Sleep(5200 * time.Millisecond))
	check(`reads.length===blockReads`)
	eval(doc + `.querySelector('.refresh-result').click()`)
	poll(doc + `.querySelector('.summary').textContent==='决策已记录'`)
	// 错误和错任务响应都停止自动重试，不把旧视图冒充新观察。
	for _, mode := range []string{"deny", "wrong"} {
		eval(`mode='` + mode + `';change('不能接收的结果');` + doc + `.querySelector('.refresh-result').click()`)
		poll(doc + `.querySelector('[data-live-state="error"]')!==null`)
		eval(`window.failureReads=reads.length`)
		run(chromedp.Sleep(1500 * time.Millisecond))
		check(`reads.length===failureReads`)
		eval(`mode='success';change('显式恢复更新');` + doc + `.querySelector('.live-toggle').click()`)
		poll(doc + `.querySelector('[data-live-state="live"]')!==null`)
		poll(doc + `.querySelector('.summary').textContent==='显式恢复更新' && !` + doc + `.body.innerText.includes('刷新失败')`)
	}
	// 悬浮在宿主提供的固定视口内，不改父文档来冒充宿主能力。
	eval(doc + `.querySelector('[data-display="pip"]').click()`)
	poll(doc + `.documentElement.dataset.displayMode==='pip'`)
	eval(`document.body.style.height='5000px';scrollTo(0,1800);change('悬浮进度可见')`)
	poll(doc + `.querySelector('.summary').textContent==='悬浮进度可见'`)
	check(`framesList[0].getBoundingClientRect().bottom<=innerHeight`)
	if dir := os.Getenv("AGENTDOCK_UI_SCREENSHOTS"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		var image []byte
		run(chromedp.CaptureScreenshot(&image))
		if err := os.WriteFile(filepath.Join(dir, "live-task-progress.png"), image, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// 完成后不轮询，即使后台任务记录又变化也需要显式刷新。
	eval(`change('任务已完成','completed')`)
	poll(doc + `.querySelector('[data-live-state="stopped"]')!==null`)
	eval(`window.completeReads=reads.length`)
	run(chromedp.Sleep(5200 * time.Millisecond))
	check(`reads.length===completeReads`)
	// 多个保留 iframe：只有可见且 active 的卡片才有后台只读更新；销毁释放 SDK。
	eval(`change('多实例检查','active');scrollTo(0,0);framesList[0].remove();framesList=[];document.getElementById('cards').replaceChildren();reads=[];addCards(10)`)
	poll(`framesList.every(f=>f.contentDocument?.querySelector('[data-entity]'))`)
	// 等待实际布局/可见性稳定后检查请求来自哪些 iframe，不能把请求总数
	// 当成活跃卡片数：可见卡片可能重复刷新，加载较慢时首轮也不保证在 1.5 秒内完成。
	eval(`framesList.forEach(f=>{f.dataset.fixedSize='true';f.style.height='430px'});scrollTo(0,0);window.visibleFrames=framesList.map((f,i)=>({i,r:f.getBoundingClientRect()})).filter(x=>x.r.bottom>0&&x.r.top<innerHeight).map(x=>x.i)`)
	check(`visibleFrames.length>0 && visibleFrames.length<10`)
	poll(`framesList.every((f,i)=>f.contentDocument.querySelector('[data-live-state]')?.dataset.liveState===(visibleFrames.includes(i)?'live':'hidden'))`)
	run(chromedp.Sleep(1000 * time.Millisecond))
	eval(`reads=[]`)
	poll(`reads.length>0`)
	run(chromedp.Sleep(5200 * time.Millisecond))
	check(`reads.every(r=>visibleFrames.includes(r.frame)) && bad.length===0`)
	t.Logf("LIVE_PROGRESS_VISIBLE_FRAMES %v observed_frames=%v", eval(`visibleFrames`), eval(`[...new Set(reads.map(r=>r.frame))]`))
	var liveUsed, released float64
	run(chromedp.ActionFunc(func(ctx context.Context) error {
		if err := heapprofiler.CollectGarbage().Do(ctx); err != nil {
			return err
		}
		var err error
		liveUsed, _, _, _, err = cdruntime.GetHeapUsage().Do(ctx)
		return err
	}))
	eval(`teardown()`)
	poll(`framesList.every(f=>f.contentDocument?.URL==='about:blank')`)
	eval(`window.finalReads=reads.length`)
	run(chromedp.Sleep(1200 * time.Millisecond))
	check(`reads.length===finalReads`)
	run(chromedp.ActionFunc(func(ctx context.Context) error {
		if err := heapprofiler.CollectGarbage().Do(ctx); err != nil {
			return err
		}
		var err error
		released, _, _, _, err = cdruntime.GetHeapUsage().Do(ctx)
		return err
	}))
	t.Logf("LIVE_PROGRESS_MEMORY active_mib=%.3f after_teardown_mib=%.3f", liveUsed/(1<<20), released/(1<<20))
	if released > liveUsed*.3 {
		t.Fatalf("live task SDK realms retained: %.1f -> %.1f", liveUsed, released)
	}
}
