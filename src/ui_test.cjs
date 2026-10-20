// Logic-only browser substitute; not real-browser or hardware QA.
const vm=require('vm'),fs=require('fs'),assert=require('assert');
const code=fs.readFileSync('index.html','utf8').split('<script>')[1].split('</script>')[0];
const noop=()=>{};
function browser(){
 const defaults={role:'battery',mode:'CV',ip:'169.254.30.1',setv:'3.7',seti:'.1',ovp:'4.3',setr:'100',esr:'尚未設定',minv:'3.5',maxv:'4.2',vstep:'.1',timeout:'5000',pollms:'1000'},nodes={};
 const element=id=>nodes[id]??={value:defaults[id]??'',textContent:'',classList:{toggle:noop},disabled:false,addEventListener:noop,setAttribute(k,v){this[k]=v},focus:noop,select:noop,clientWidth:600,clientHeight:240,options:['CV','CC','CR'].map(value=>({value,disabled:false})),getContext:()=>new Proxy({},{get:()=>noop})};
 const ctx={document:{getElementById:element,createElement:()=>({click:noop})},location:{hash:'',pathname:'/'},sessionStorage:{getItem:()=>null,setItem:noop},history:{replaceState:noop},window:{addEventListener:noop},devicePixelRatio:1,setTimeout:noop,clearTimeout:noop,setInterval:noop,Blob,URL,console};
 vm.createContext(ctx);vm.runInContext(code,ctx);
 return {ctx,element,run:source=>vm.runInContext(source,ctx)};
}
const response=value=>({status:200,ok:true,text:async()=>JSON.stringify(value)});
const flush=()=>new Promise(resolve=>setImmediate(resolve));
const reading={output:false,voltage:3.7,current:0,power:0,emulation:'PS2Q',actual:'OFF',sample_ms:0,time:'now'};
async function tests(){
 const b=browser(),e=b.element;
 assert.equal(e('role').value,'battery');assert.equal(e('mode').value,'CV');assert.equal(e('apply').disabled,false);assert.equal(e('ip').value,'169.254.30.1');
 b.ctx.fetch=()=>{throw Error('draft controls must not contact the instrument')};
 for(const n of [1,2,3,4]){e(`preset-battery-${n}`).onclick();for(const [id,v] of Object.entries({setv:3.7,minv:3.5,maxv:4.2,ovp:4.3}))assert(Math.abs(+e(id).value-v*n)<1e-6)}
 for(const n of [1,2,4,8]){e(`preset-io-${n}`).onclick();assert.equal(+e('seti').value,n)}
 for(const n of [0,10,100]){e(`preset-esr-${n}`).onclick();assert.equal(e('esr').value,'尚未設定');assert(e(`preset-esr-${n}`).disabled)}
 b.run('state={connected:true,config:params(),reading:{output:false}};controls()');assert.equal(e('on').disabled,false);
 // Busy and ON states permit editing the next draft while blocking ON/apply.
 for(const source of ['state.reading.output=true','state.reading.output=false;working=true','working=false;state.busy=true']){
  b.run(source+';controls()');assert.equal(e('preset-battery-1').disabled,false);assert.equal(e('setv').disabled,false);
  e('preset-battery-1').onclick();assert.equal(+e('setv').value,3.7);assert.equal(e('on').disabled,true);
 }
 b.run('working=false;state={connected:true,reading:{output:false},config:params()};controls()');
 e('vup').onclick();assert.equal(+e('setv').value,3.8);assert.equal(e('on').disabled,true);
 e('vdown').onclick();assert.equal(+e('setv').value,3.7);
 e('setv').value='4.2';e('vup').onclick();assert.equal(+e('setv').value,4.2);
 e('setv').value='3.5';e('vdown').onclick();assert.equal(+e('setv').value,3.5);
 e('role').value='sink';e('mode').value='CR';b.run('adjust()');assert.equal(e('seti').disabled,true);assert.equal(e('preset-io-1').disabled,true);
 b.run('state={connected:true,debug_only:true,reading:{output:false},config:params()};controls()');assert.equal(e('on').disabled,true);assert.equal(e('apply').disabled,false);
 b.run('unavailable("lost")');assert.equal(e('on').disabled,true);assert.equal(e('apply').disabled,false);
 // Clipboard snapshot and denial fallback work even after losing local service.
 b.ctx.rows=Array.from({length:100},(_,i)=>'TX/RX '+i);b.run('state={version:"v0.5.0",logs:rows}');
 let copied='';b.ctx.navigator={clipboard:{writeText:async text=>{copied=text}}};await e('copyRecent').onclick();assert(copied.includes('TX/RX 20\n'));assert(!copied.includes('TX/RX 19\n'));assert(copied.endsWith('TX/RX 99'));
 b.ctx.navigator.clipboard.writeText=async()=>{throw Error('denied')};e('logs').textContent=b.ctx.rows.join('\n');b.run('state=null');await e('copyRecent').onclick();assert.equal(e('copyText').value,copied);
 // Bootstrap auto-connects once, without configuring or enabling output.
 const a=browser(),calls=[];
 a.ctx.fetch=async(path,options)=>{if(path==='/api/action'){calls.push(JSON.parse(options.body));return response({message:'connected'})}return response({connected:true,reading,logs:[],preferences:{},config:null})};
 a.run('token="t";hydrated=true;maybeAutoConnect();maybeAutoConnect()');await flush();await flush();
 assert.deepEqual(calls.map(x=>x.action),['connect']);assert.equal(a.element('apply').disabled,false);
 // A slow prepare sends a fixed snapshot; OFF keeps its own pending state.
 const w=browser(),actions=[];let finishPrepare,finishOff;
 w.ctx.fetch=async(path,options)=>{
  if(path==='/api/action'){
   const sent=JSON.parse(options.body);actions.push(sent);
   if(sent.action==='prepare')return new Promise(resolve=>{finishPrepare=()=>resolve(response({message:'cancelled',cancelled:true}))});
   if(sent.action==='off')return new Promise(resolve=>{finishOff=()=>resolve(response({message:'OFF'}))});
  }
  return response({connected:true,reading,logs:[],config:null});
 };
 const preparing=w.run('action("prepare")');const sentVoltage=actions[0].config.voltage;
 w.element('preset-battery-3').onclick();assert.equal(+w.element('setv').value,11.1);assert.equal(actions[0].config.voltage,sentVoltage);
 const stopping=w.run('action("off")');assert.equal(w.element('off').disabled,true);
 finishPrepare();await preparing;await flush();assert.equal(w.run('working'),true);assert.equal(w.element('apply').disabled,true);
 finishOff();await stopping;await flush();assert.equal(w.run('working'),false);assert.equal(w.element('apply').disabled,false);
 assert.deepEqual(actions.map(x=>x.action),['prepare','off']);assert.equal(actions[0].config.role,'battery');
 console.log('PASS UI logic: default battery, draft presets and V steps during ON/busy, ON explanations, one-shot read-only autoconnect, immutable prepare payload, concurrent OFF lifecycle, copy fallback');
}
tests().catch(error=>{console.error(error);process.exitCode=1});
