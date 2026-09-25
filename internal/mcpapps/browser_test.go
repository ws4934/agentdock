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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// 隔离宿主只服务合成数据，不接管用户浏览器，不调用实际 MCP 工具。
const browserHost = `<!doctype html><meta charset="utf-8"><style>body{margin:0;background:#fafafa}iframe{width:100%%;height:120px;border:0;display:block}</style><iframe sandbox="allow-scripts allow-same-origin"></iframe><script>
const frame=document.querySelector('iframe'); let mode=%s; const html=%s, data=%s;
window.messages=[];window.errors=[];window.refreshMode='success';window.pendingRefresh=null;
window.displayBehavior='requested';window.messageBehavior='accept';window.displayMode='inline';
if(mode==='interactive'){
 data.work_result.task.id='tsk_0123456789abcdef';data.work_result.task.status='active';
 data.work_result.selection.task_id=data.work_result.task.id;
 data.work_result.task.steps=[{id:'verify',title:'Validate without replay',status:'in_progress'}];
}
window.send=(method,params)=>frame.contentWindow.postMessage({jsonrpc:'2.0',method,params},'*');
window.result=d=>send('ui/notifications/tool-result',{structuredContent:d,content:[]});
window.replyRefresh=(request)=>{
 let projection=JSON.parse(JSON.stringify(data.work_result||{}));
 projection.observed_at='refreshed';
 if(refreshMode==='wrong-scope')projection.task.id='another-task';
 const result=refreshMode==='failed'?{isError:true,content:[{type:'text',text:'Synthetic read failure'}]}:{structuredContent:{work_result:projection},content:[]};
 frame.contentWindow.postMessage({jsonrpc:'2.0',id:request.id,result},'*');
};
window.teardown=()=>frame.contentWindow.postMessage({jsonrpc:'2.0',id:801,method:'ui/resource-teardown',params:{}},'*');
window.addEventListener('message',e=>{
 if(e.source!==frame.contentWindow)return;const m=e.data;messages.push(m);
 if(m.method==='ui/initialize'){
  if(mode==='timeout')return;
  const reply=mode==='init-error'?{error:{code:-32000,message:'Synthetic init error'}}:{result:{protocolVersion:'2026-01-26',hostInfo:{name:'isolated-test-host',version:'1.0'},hostCapabilities:{openLinks:{},serverTools:{}},hostContext:{theme:'light',locale:'zh-CN'}}};
  if(mode==='interactive'){
   reply.result.hostCapabilities.message={text:{}};
   reply.result.hostContext.availableDisplayModes=['inline','fullscreen','pip'];
   reply.result.hostContext.displayMode='inline';
  }
  frame.contentWindow.postMessage({jsonrpc:'2.0',id:m.id,...reply},'*');
 }
 if(m.method==='ui/notifications/initialized'){
  send('ui/notifications/tool-input',{arguments:{}});
  if(mode==='cancelled'){send('ui/notifications/tool-cancelled',{reason:'Synthetic cancellation'});return;}
  if(mode==='error'){send('ui/notifications/tool-result',{isError:true,structuredContent:{error:'Synthetic failure'},content:[]});return;}
  if(mode==='content-only'){send('ui/notifications/tool-result',{content:[{type:'text',text:JSON.stringify(data)}]});return;}
  if(mode==='empty'){send('ui/notifications/tool-result',{content:[]});return;}
  result(data);
 }
 if(m.method==='ui/notifications/size-changed'&&displayMode==='inline')frame.style.height=m.params.height+'px';
 if(m.method==='ui/request-display-mode'){
  if(displayBehavior==='deny'){frame.contentWindow.postMessage({jsonrpc:'2.0',id:m.id,error:{code:-32000,message:'Synthetic mode denial'}},'*');return;}
  displayMode=displayBehavior==='fullscreen'?'fullscreen':m.params.mode;
  frame.style.cssText=displayMode==='pip'?'position:fixed;right:12px;bottom:12px;width:360px;height:360px;z-index:2;background:white':displayMode==='fullscreen'?'position:fixed;inset:0;width:100vw;height:100vh;z-index:2;background:white':'';
  frame.contentWindow.postMessage({jsonrpc:'2.0',id:m.id,result:{mode:displayMode}},'*');
 }
 if(m.method==='ui/message')frame.contentWindow.postMessage({jsonrpc:'2.0',id:m.id,result:{isError:messageBehavior==='deny'}},'*');
 if(m.method==='ui/open-link')frame.contentWindow.postMessage({jsonrpc:'2.0',id:m.id,result:{}},'*');
 if(m.method==='tools/call'){
  if(m.params.name!=='work_result_read'){errors.push('Unexpected mutation tool');return;}
  if(refreshMode==='hold')pendingRefresh=m;else replyRefresh(m);
 }
});
frame.addEventListener('load',()=>frame.contentWindow.addEventListener('error',e=>errors.push(e.message)));
frame.srcdoc=html;
</script>`

const auditBootstrap = `
window.__audit={listeners:0,observers:0,timers:new Set(),frames:new Set()};
const originalAdd=window.addEventListener,originalRemove=window.removeEventListener;
window.addEventListener=function(type,...args){if(type==='message')__audit.listeners++;return originalAdd.call(this,type,...args)};
window.removeEventListener=function(type,...args){if(type==='message')__audit.listeners--;return originalRemove.call(this,type,...args)};
const RO=window.ResizeObserver;
window.ResizeObserver=class extends RO{active=false;observe(...args){if(!this.active){this.active=true;__audit.observers++}return super.observe(...args)}disconnect(){if(this.active){this.active=false;__audit.observers--}return super.disconnect()}};
const timeout=window.setTimeout,clear=window.clearTimeout,raf=window.requestAnimationFrame,caf=window.cancelAnimationFrame;
window.setTimeout=function(fn,delay,...args){let id=timeout(()=>{__audit.timers.delete(id);fn(...args)},delay);__audit.timers.add(id);return id};
window.clearTimeout=function(id){__audit.timers.delete(id);return clear(id)};
window.requestAnimationFrame=function(fn){let id=raf(t=>{__audit.frames.delete(id);fn(t)});__audit.frames.add(id);return id};
window.cancelAnimationFrame=function(id){__audit.frames.delete(id);return caf(id)};
`

var fixtures = map[string]any{
	"work_result":       map[string]any{"work_result": map[string]any{"task": map[string]any{"id": "work-fixture", "title": "冻结交付验证", "final_review": map[string]any{"status": "pass"}}, "workdir": "/synthetic/project", "observed_at": "initial", "source": map[string]any{"complete": true, "revision": "src1:fixture", "scope": []string{"source.go"}}, "selection": map[string]any{"task_id": "work-fixture", "workdir": "/synthetic/project", "source_paths": []string{"source.go"}}, "frozen": false, "validation": "not_verified", "changes": []string{"M source.go"}, "jobs": []any{}, "artifacts": []any{}}},
	"agentdock_context": map[string]any{"skills": []any{map[string]any{"name": "macos-desktop", "description": "原生桌面能力"}, map[string]any{"name": "<img src=x onerror=alert(1)>", "description": "不可信数据必须作为文本"}}, "dynamic_mcp": []any{map[string]any{"name": "pencil", "status": "ready"}}},
	"task_progress":     map[string]any{"action": "get", "task_id": "test-task", "task_summary": map[string]any{"id": "test-task", "title": "验证反馈组件", "summary": "初始阶段", "status": "active", "step_count": 3, "completed_step_count": 1, "steps": []any{map[string]any{"id": "one", "title": "建立连接", "status": "completed"}, map[string]any{"id": "two", "title": "浏览器验收", "status": "in_progress"}, map[string]any{"id": "three", "title": "生成安装包", "status": "pending"}}}},
	"file_change":       map[string]any{"action": "patch", "files_changed": 2, "summary": "更新布局与状态处理", "insertions": 32, "deletions": 8, "diff_preview": "--- a/main.ts\n+++ b/main.ts\n-old\n+new"},
	"dynamic_mcp":       map[string]any{"name": "pencil:inspect", "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "读取成功"}}}},
	"artifact":          map[string]any{"artifact_id": "test-artifact", "filename": "AgentDock.dmg", "url": "https://example.test/file.dmg", "size_bytes": 33000000, "sha256": "synthetic"},
	"workflow":          map[string]any{"action": "match", "candidates": []any{map[string]any{"id": "a", "title": "发布检查", "description": "执行验证后发布"}}},
	"recall":            map[string]any{"recall_action": "write", "recall": map[string]any{"id": "test-recall", "title": "项目约定", "content": "保持签名身份一致"}},
	"acp_status":        map[string]any{"action": "get", "session": map[string]any{"id": "test-session", "agent": "Coding Agent", "status": "ready"}, "messages": []any{map[string]any{"role": "assistant", "content": "已完成修改"}}},
}

// 必须先让 Chrome 正常关闭并排空自身资源，再取消 allocator/清理用户目录。
// 仅 context cancel 会强制结束浏览器，配置写入子进程可能与 TempDir 清理竞争。
func closeIsolatedBrowser(t *testing.T, browser context.Context) {
	t.Helper()
	shutdown, cancel := context.WithTimeout(browser, 10*time.Second)
	defer cancel()
	if err := chromedp.Cancel(shutdown); err != nil {
		t.Errorf("graceful isolated Chrome shutdown: %v", err)
	}
}

func TestFeedbackBrowser(t *testing.T) {
	binary := os.Getenv("AGENTDOCK_UI_CHROME")
	if binary == "" && runtime.GOOS == "darwin" {
		binary = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	if binary == "" {
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium"} {
			if p, err := exec.LookPath(name); err == nil {
				binary = p
				break
			}
		}
	}
	if binary == "" {
		t.Fatal("set AGENTDOCK_UI_CHROME to an installed Chrome executable")
	}
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(binary), chromedp.UserDataDir(t.TempDir()), chromedp.Flag("disable-background-networking", true), chromedp.Flag("disable-extensions", true))
	alloc, stopAlloc := chromedp.NewExecAllocator(t.Context(), options...)
	defer stopAlloc()
	browser, stopBrowser := chromedp.NewContext(alloc)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 150*time.Second)
	defer cancel()
	defer closeIsolatedBrowser(t, browser)
	j := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		view := r.URL.Query().Get("view")
		if _, ok := resources[view]; !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := strings.Replace(HTML(view, ""), "<script>", "<script>"+auditBootstrap+"</script><script>", 1)
		fmt.Fprintf(w, browserHost, j(r.URL.Query().Get("mode")), j(html), j(fixtures[view]))
	}))
	defer server.Close()
	run := func(actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			t.Fatal(err)
		}
	}
	eval := func(script string) any { t.Helper(); var v any; run(chromedp.Evaluate(script, &v)); return v }
	check := func(script string) {
		t.Helper()
		if eval(script) != true {
			t.Fatalf("browser assertion failed: %s; body=%v errors=%v", script, eval(`document.querySelector('iframe').contentDocument.body.innerText`), eval(`window.errors`))
		}
	}
	open := func(view, mode string) {
		run(chromedp.Navigate(server.URL + "/?view=" + view + "&mode=" + mode))
		run(chromedp.Poll(`document.querySelector('iframe')?.contentDocument?.querySelector('.card')!==null`, nil, chromedp.WithPollingTimeout(5*time.Second)))
	}
	ready := func() {
		run(chromedp.Poll(`!!document.querySelector('iframe').contentDocument.querySelector('[data-entity]')`, nil, chromedp.WithPollingTimeout(5*time.Second)))
	}
	doc := `document.querySelector('iframe').contentDocument`
	run(chromedp.EmulateViewport(640, 800))
	for _, view := range []string{"agentdock_context", "task_progress", "file_change", "dynamic_mcp", "artifact", "workflow", "recall", "acp_status", "work_result"} {
		open(view, "normal")
		ready()
		check(`window.errors.length===0`)
		check(`Array.from(` + doc + `.querySelector('.card').childNodes).every(n=>n.nodeType!==3||n.textContent.trim()==='')`)
		check(doc + `.querySelectorAll('.details li').length===0`)
		check(doc + `.documentElement.lang==='zh-CN'`)
		if eval(`!!`+doc+`.querySelector('.toggle')`) == true {
			eval(doc + `.querySelector('.toggle').click()`)
			run(chromedp.Sleep(60 * time.Millisecond))
			check(doc + `.querySelector('.toggle').getAttribute('aria-expanded')==='true'`)
		}
		if dir := os.Getenv("AGENTDOCK_UI_SCREENSHOTS"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			var image []byte
			run(chromedp.FullScreenshot(&image, 100))
			if err := os.WriteFile(filepath.Join(dir, view+".png"), image, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, mode := range []string{"error", "cancelled", "empty", "content-only", "init-error", "timeout"} {
		open("agentdock_context", mode)
		if mode == "content-only" {
			ready()
			continue
		}
		phase := mode
		if mode == "init-error" {
			phase = "error"
		}
		selector := fmt.Sprintf(`[data-phase="%s"]`, phase)
		if mode == "empty" {
			run(chromedp.Poll(`!!`+doc+`.querySelector('[data-entity="fallback"]')`, nil))
			continue
		}
		run(chromedp.Poll(`!!`+doc+`.querySelector(`+j(selector)+`)`, nil, chromedp.WithPollingTimeout(12*time.Second)))
		check(`!` + doc + `.body.innerText.includes('Waiting for tool output')`)
	}
	// 更新保持展开、焦点；重复结果不产生 DOM 修改或重复尺寸通知。
	open("agentdock_context", "init-error")
	run(chromedp.Poll(`!!`+doc+`.querySelector('[data-phase="error"]')`, nil))
	eval(`mode='normal'`)
	for i := 0; i < 10; i++ {
		eval(doc + `.querySelector('.text-button').click()`)
		ready()
		check(`frame.contentWindow.__audit.listeners===1 && frame.contentWindow.__audit.observers===1`)
		if i < 9 {
			eval(`send('ui/notifications/tool-result',{isError:true,content:[{type:'text',text:'Retry fixture'}]})`)
			run(chromedp.Poll(`!!`+doc+`.querySelector('[data-phase="error"]')`, nil))
		}
	}
	run(chromedp.Sleep(200 * time.Millisecond))
	check(`frame.contentWindow.__audit.timers.size===0 && frame.contentWindow.__audit.frames.size===0`)
	check(`!messages.some(m=>m.method==='tools/call')`)
	// 非父窗口伪造的通知不得覆盖结果。
	eval(`frame.contentWindow.dispatchEvent(new frame.contentWindow.MessageEvent('message',{source:frame.contentWindow,data:{jsonrpc:'2.0',method:'ui/notifications/tool-result',params:{isError:true,content:[]}}}))`)
	run(chromedp.Sleep(60 * time.Millisecond))
	check(`!!` + doc + `.querySelector('[data-entity]')`)
	// Fleet 设备选择在结果刷新后保持。
	eval(`window.fleet={nodes:[{node_id:'a',name:'A',online:true,context:{skills:[{name:'one'}]}},{node_id:'b',name:'B',online:true,context:{skills:[{name:'two'}]}}]};result(fleet)`)
	run(chromedp.Poll(doc+`.querySelector('[data-entity]')?.getAttribute('data-entity')==='context-fleet'`, nil))
	eval(doc + `.querySelector('.toggle').click()`)
	run(chromedp.Poll(`!!`+doc+`.querySelector('select')`, nil))
	eval(`const s=` + doc + `.querySelector('select');s.value='b';s.dispatchEvent(new frame.contentWindow.Event('change',{bubbles:true}));fleet.nodes[1].context.skills[0].name='updated';result(fleet)`)
	run(chromedp.Poll(doc+`.body.innerText.includes('updated')`, nil))
	check(doc + `.querySelector('select').value==='b'`)
	open("task_progress", "normal")
	ready()
	eval(doc + `.querySelector('.toggle').click()`)
	run(chromedp.Sleep(80 * time.Millisecond))
	eval(doc + `.querySelector('.toggle').focus()`)
	eval(`window.next=JSON.parse(` + j(j(fixtures["task_progress"])) + `);next.task_summary.summary='更新阶段';result(next)`)
	run(chromedp.Poll(doc+`.querySelector('.summary').textContent==='更新阶段'`, nil))
	check(doc + `.querySelector('.toggle').getAttribute('aria-expanded')==='true'`)
	check(doc + `.activeElement===` + doc + `.querySelector('.toggle')`)
	// Establish the duplicate-result baseline only after the prior real update's
	// resize notification has reached the host. A fixed sleep races slow frames.
	run(chromedp.Evaluate(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`, nil, func(p *cdruntime.EvaluateParams) *cdruntime.EvaluateParams { return p.WithAwaitPromise(true) }))
	run(chromedp.Poll(`frame.contentWindow.__audit.frames.size===0 && messages.filter(m=>m.method==='ui/notifications/size-changed').at(-1)?.params.height===Math.ceil(`+doc+`.getElementById('content').getBoundingClientRect().height)`, nil))
	eval(`window.mutations=0;window.mo=new MutationObserver(rs=>mutations+=rs.length);mo.observe(` + doc + `.getElementById('content'),{subtree:true,childList:true,attributes:true,characterData:true});window.beforeSizes=messages.filter(m=>m.method==='ui/notifications/size-changed').length;for(let i=0;i<1000;i++)result(next)`)
	run(chromedp.Sleep(1200 * time.Millisecond))
	check(`window.mutations===0`)
	check(`messages.filter(m=>m.method==='ui/notifications/size-changed').length===beforeSizes`)
	eval(`mo.disconnect()`)
	// 延迟以实际 DOM 提交为界，不把两帧绘制等待计入计算时间。
	var perf map[string]any
	run(chromedp.Evaluate(`(async()=>{const times=[];for(let i=0;i<40;i++){const value='阶段 '+i;const start=performance.now();await new Promise(resolve=>{const m=new MutationObserver(()=>{if(`+doc+`.querySelector('.summary').textContent===value){m.disconnect();resolve()}});m.observe(`+doc+`.getElementById('content'),{subtree:true,childList:true,characterData:true});next.task_summary.summary=value;result(next)});times.push(performance.now()-start)}times.sort((a,b)=>a-b);return {p95_ms:times[Math.floor(times.length*.95)],max_ms:times.at(-1),samples:times.length}})()`, &perf, func(p *cdruntime.EvaluateParams) *cdruntime.EvaluateParams { return p.WithAwaitPromise(true) }))
	t.Logf("progress commit performance: %v", perf)
	if perf["p95_ms"].(float64) > 16 {
		t.Errorf("progress commit p95 exceeds 16ms: %v", perf)
	}
	// 错误后不保留之前的成功卡片。
	eval(`send('ui/notifications/tool-result',{isError:true,structuredContent:{error:'Late failure'},content:[]})`)
	run(chromedp.Poll(`!!`+doc+`.querySelector('[data-phase="error"]')`, nil))
	check(`!` + doc + `.querySelector('[data-entity]')`)
	// 主题、窄屏、文本注入、安全 URL 与取消。
	open("agentdock_context", "normal")
	ready()
	eval(`send('ui/notifications/host-context-changed',{theme:'dark',locale:'en'})`)
	run(chromedp.Poll(doc+`.documentElement.dataset.theme==='dark'`, nil))
	check(doc + `.documentElement.lang==='en'`)
	run(chromedp.EmulateViewport(240, 800))
	eval(doc + `.querySelector('.toggle').click()`)
	run(chromedp.Sleep(100 * time.Millisecond))
	check(doc + `.documentElement.scrollWidth<=240`)
	check(doc + `.querySelectorAll('img,svg,script[src]').length===0`)
	eval(doc + `.body.style.zoom='2'`)
	run(chromedp.Sleep(100 * time.Millisecond))
	check(doc + `.documentElement.scrollWidth<=240`)
	open("artifact", "normal")
	ready()
	eval(`result({artifact_id:'x',filename:'Unsafe',url:'javascript:alert(1)'})`)
	run(chromedp.Poll(doc+`.querySelector('h2').textContent==='Unsafe'`, nil))
	check(`!` + doc + `.querySelector('.primary')`)
	eval(`result({artifact_id:'x',filename:'Expired',url:'https://example.test/file',expires_at:'2000-01-01T00:00:00Z'})`)
	run(chromedp.Poll(doc+`.querySelector('h2').textContent==='Expired'`, nil))
	check(doc + `.querySelector('.primary').disabled`)
	// 销毁后停止渲染且不得发业务调用；重连仅恢复桥接。
	eval(`frame.contentWindow.postMessage({jsonrpc:'2.0',id:800,method:'ui/resource-teardown',params:{}},'*')`)
	run(chromedp.Poll(doc+`.URL==='about:blank'`, nil))
	eval(`result({filename:'After teardown'})`)
	run(chromedp.Sleep(100 * time.Millisecond))
	check(doc + `.body.childElementCount===0`)
	check(`messages.some(m=>m.id===800 && m.result && !m.error) && frame.contentWindow.__audit===undefined`)
	check(`!messages.some(m=>m.method==='tools/call'||m.method==='ui/message')`)

	// 文件操作结果自动挂载原卡片；预演、无变更和错误不得伪装成已写入。
	open("file_change", "normal")
	ready()
	for _, action := range []string{"add", "replace", "patch", "move", "delete"} {
		eval(`result({action:` + j(action) + `,path:'fixture.txt',files_changed:1,changed:true,dry_run:true,diff_preview:'-old\n+new'})`)
		run(chromedp.Poll(doc+`.querySelector('h2').textContent==='变更预览'`, nil))
		check(doc + `.querySelector('.card').dataset.tone==='neutral' && ` + doc + `.body.innerText.includes('尚未写入')`)
		eval(`result({action:` + j(action) + `,path:'fixture.txt',files_changed:1,changed:true,diff_preview:'-old\n+new'})`)
		run(chromedp.Poll(doc+`.querySelector('.card').dataset.tone==='success'`, nil))
	}
	eval(`result({action:'replace',path:'fixture.txt',files_changed:0,changed:false})`)
	run(chromedp.Poll(doc+`.querySelector('h2').textContent==='没有文件变更'`, nil))
	check(doc + `.querySelector('.card').dataset.tone==='neutral'`)
	eval(`send('ui/notifications/tool-result',{isError:true,structuredContent:{error:'版本冲突，请重新读取'},content:[]})`)
	run(chromedp.Poll(`!!`+doc+`.querySelector('[data-phase="error"]')`, nil))
	check(`!` + doc + `.querySelector('[data-entity]') && !messages.some(m=>m.method==='tools/call'||m.method==='ui/message')`)

	// Work results refresh only on explicit user input, use the read tool, and
	// reject stale in-flight responses or results belonging to another task.
	open("work_result", "normal")
	ready()
	check(`!messages.some(m=>m.method==='tools/call')`)
	eval(doc + `.querySelector('.toggle').click()`)
	eval(doc + `.querySelector('.refresh-result').click()`)
	run(chromedp.Poll(doc+`.body.innerText.includes('refreshed')`, nil))
	check(doc + `.querySelector('.toggle').getAttribute('aria-expanded')==='true'`)
	check(`messages.filter(m=>m.method==='tools/call').length===1 && messages.find(m=>m.method==='tools/call').params.name==='work_result_read'`)
	for _, mode := range []string{"failed", "wrong-scope"} {
		eval(`refreshMode=` + j(mode))
		eval(doc + `.querySelector('.refresh-result').click()`)
		run(chromedp.Poll(doc+`.body.innerText.includes('刷新失败')`, nil))
		check(doc + `.querySelector('[data-entity]').getAttribute('data-entity')==='work-fixture'`)
	}
	eval(`refreshMode='hold'`)
	eval(doc + `.querySelector('.refresh-result').click()`)
	run(chromedp.Poll(`pendingRefresh!==null`, nil))
	eval(`window.newer=JSON.parse(JSON.stringify(data));newer.work_result.observed_at='newer-host-result';result(newer)`)
	run(chromedp.Poll(doc+`.body.innerText.includes('newer-host-result')`, nil))
	eval(`refreshMode='success';replyRefresh(pendingRefresh)`)
	run(chromedp.Poll(doc+`.body.innerText.includes('刷新失败')`, nil))
	check(doc + `.body.innerText.includes('newer-host-result')`)
	eval(`newer.work_result.frozen=true;newer.work_result.result_id='frozen-fixture';result(newer)`)
	run(chromedp.Poll(`!`+doc+`.querySelector('.refresh-result')`, nil))
	check(`messages.filter(m=>m.method==='tools/call').every(m=>m.params.name==='work_result_read')`)

	// 宿主决定悬浮/全屏和页面滚动；组件不触碰父页面，消息必须显式点击且不自动重放。
	run(chromedp.EmulateViewport(960, 800))
	open("work_result", "interactive")
	ready()
	run(chromedp.Poll(`!!`+doc+`.querySelector('[data-display="pip"]')`, nil))
	eval(doc + `.querySelector('[data-display="pip"]').click()`)
	run(chromedp.Poll(doc+`.documentElement.dataset.displayMode==='pip'`, nil))
	eval(`document.body.style.height='3000px';window.scrollTo(0,1600)`)
	check(`window.scrollY>1000 && frame.getBoundingClientRect().bottom<=innerHeight && frame.getBoundingClientRect().top>0`)
	check(doc + `.documentElement.scrollWidth<=360`)
	eval(doc + `.querySelector('.resume-help').click()`)
	run(chromedp.Poll(`!!`+doc+`.querySelector('.resume-prompt')`, nil))
	check(doc + `.querySelector('.resume-prompt').value.includes('tsk_0123456789abcdef')`)
	eval(doc + `.querySelector('[data-display="inline"]').click()`)
	run(chromedp.Poll(doc+`.documentElement.dataset.displayMode==='inline'`, nil))
	eval(`displayBehavior='fullscreen'`)
	eval(doc + `.querySelector('[data-display="pip"]').click()`)
	run(chromedp.Poll(doc+`.documentElement.dataset.displayMode==='fullscreen'`, nil))
	check(`frame.getBoundingClientRect().height===innerHeight`)
	eval(`displayBehavior='requested'`)
	eval(doc + `.querySelector('[data-display="inline"]').click()`)
	run(chromedp.Poll(doc+`.documentElement.dataset.displayMode==='inline'`, nil))
	eval(`displayBehavior='deny'`)
	eval(doc + `.querySelector('[data-display="fullscreen"]').click()`)
	run(chromedp.Poll(doc+`.body.innerText.includes('宿主未确认显示切换')`, nil))
	check(doc + `.documentElement.dataset.displayMode==='inline'`)
	eval(doc + `.querySelector('.continue-task').click();` + doc + `.querySelector('.continue-task').click()`)
	run(chromedp.Poll(doc+`.body.innerText.includes('续接请求已交给宿主')`, nil))
	check(`messages.filter(m=>m.method==='ui/message').length===1 && !messages.some(m=>m.method==='tools/call')`)
	check(`messages.find(m=>m.method==='ui/message').params.role==='user'`)
	check(doc + `.querySelector('.continue-task').disabled`)
	if dir := os.Getenv("AGENTDOCK_UI_SCREENSHOTS"); dir != "" {
		eval(`displayBehavior='requested';window.scrollTo(0,0)`)
		eval(doc + `.querySelector('[data-display="pip"]').click()`)
		run(chromedp.Poll(doc+`.documentElement.dataset.displayMode==='pip'`, nil))
		var image []byte
		run(chromedp.CaptureScreenshot(&image))
		if err := os.WriteFile(filepath.Join(dir, "work-result-pip.png"), image, 0600); err != nil {
			t.Fatal(err)
		}
	}
	open("work_result", "interactive")
	ready()
	run(chromedp.Poll(`!!`+doc+`.querySelector('.continue-task')`, nil))
	eval(`messageBehavior='deny'`)
	eval(doc + `.querySelector('.continue-task').click()`)
	run(chromedp.Poll(doc+`.body.innerText.includes('无法确认续接消息是否送达')`, nil))
	check(doc + `.querySelector('.continue-task').disabled`)
	check(`messages.filter(m=>m.method==='ui/message').length===1 && !messages.some(m=>m.method==='tools/call')`)
	eval(`teardown()`)
	run(chromedp.Poll(doc+`.URL==='about:blank'`, nil))
	check(`frame.contentWindow.__audit===undefined`)

	// 同一页面同时承载九类卡片：记录冷挂载耗时，并检查全部卡片闲置后不再发消息。
	run(chromedp.EmulateViewport(960, 900))
	eval(`window.batch=[];window.batchStarted=performance.now();document.body.replaceChildren();for(const view of ['agentdock_context','task_progress','file_change','dynamic_mcp','artifact','workflow','recall','acp_status','work_result']){const f=document.createElement('iframe');f.style.height='160px';f.src=` + j(server.URL) + `+'/?view='+view+'&mode=normal';batch.push(f);document.body.append(f)}`)
	run(chromedp.Poll(`batch.every(f=>f.contentDocument?.querySelector('iframe')?.contentDocument?.querySelector('[data-entity]'))`, nil, chromedp.WithPollingTimeout(15*time.Second)))
	t.Logf("nine simultaneous views mounted in %v ms (local fixture transport)", eval(`performance.now()-batchStarted`))
	run(chromedp.Sleep(400 * time.Millisecond))
	eval(`window.batchCounts=batch.map(f=>f.contentWindow.messages.length)`)
	run(chromedp.Sleep(800 * time.Millisecond))
	check(`batch.every((f,i)=>{const h=f.contentWindow,u=h.document.querySelector('iframe').contentWindow;return h.messages.length===batchCounts[i] && u.__audit.listeners===1 && u.__audit.observers===1 && u.__audit.timers.size===0 && u.__audit.frames.size===0})`)
	eval(`for(const f of batch){f.contentWindow.teardown()}`)
	run(chromedp.Poll(`batch.every(f=>f.contentDocument.querySelector('iframe').contentDocument.URL==='about:blank')`, nil))
	check(`batch.every(f=>{const u=f.contentDocument.querySelector('iframe').contentWindow;return u.__audit===undefined && f.contentWindow.messages.some(m=>m.id===801 && m.result && !m.error)})`)
}
