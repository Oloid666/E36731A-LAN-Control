// Logic-only browser substitute. This is not visual/browser QA.
const vm=require('vm'),fs=require('fs'),assert=require('assert');
const defaults={role:'source',mode:'CV',ip:'169.254.30.1',setv:'3.7',seti:'.1',ovp:'4.3',setr:'100',esr:'10',minv:'3.5',maxv:'4.2',previewi:'1',timeout:'5000',pollms:'1000'};
const nodes={};const noop=()=>{};
function element(id){return nodes[id]??={value:defaults[id]??'',textContent:'',classList:{toggle:noop},disabled:false,addEventListener:noop,setAttribute(key,value){this[key]=value},clientWidth:600,clientHeight:240,options:['CV','CC','CR','ESR'].map(value=>({value,disabled:false})),getContext:()=>new Proxy({},{get:()=>noop})}}
const code=fs.readFileSync('index.html','utf8').split('<script>')[1].split('</script>')[0];
const context={document:{getElementById:element,createElement:()=>({click:noop})},location:{hash:'',pathname:'/'},sessionStorage:{getItem:()=>null,setItem:noop},history:{replaceState:noop},window:{addEventListener:noop},devicePixelRatio:1,setTimeout:noop,setInterval:noop,Blob,URL,console};
vm.createContext(context);vm.runInContext(code,context);
assert.equal(element('ip').value,'169.254.30.1');
element('role').value='battery';vm.runInContext('adjust()',context);
assert.equal(element('mode').value,'ESR');assert.equal(element('esr').value,'10');
assert(element('esrpreview').textContent.includes('3.690000'));
element('role').value='sink';element('mode').value='CR';vm.runInContext('adjust()',context);
assert.equal(element('setr').disabled,false);assert.equal(element('seti').disabled,true);
vm.runInContext(`state={connected:true,debug_only:true,reading:{output:false},config:params()};controls()`,context);
assert.equal(element('on').disabled,true);assert.equal(element('apply').disabled,true);
vm.runInContext(`state={connected:true,debug_only:false,reading:{output:false},config:params()};controls()`,context);
assert.equal(element('on').disabled,false);
element('setr').value='101';vm.runInContext('controls()',context);assert.equal(element('on').disabled,true);
vm.runInContext('unavailable("lost")',context);assert.equal(element('actual').textContent,'未知');assert.equal(element('on').disabled,true);
// Presets change only draft values; any accidental network call fails this test.
context.fetch=()=>{throw Error('presets must not contact the instrument')};
element('role').value='battery';vm.runInContext('adjust()',context);
for(const n of [1,2,3,4]){
 element(`preset-battery-${n}`).onclick();
 for(const [id,v] of Object.entries({setv:3.7,minv:3.5,maxv:4.2,ovp:4.3}))assert(Math.abs(+element(id).value-v*n)<1e-6);
 assert.equal(element(`preset-battery-${n}`)['aria-pressed'],'true');
}
for(const n of [1,2,4,8]){element(`preset-io-${n}`).onclick();assert.equal(+element('seti').value,n)}
for(const n of [0,10,100]){element(`preset-esr-${n}`).onclick();assert.equal(+element('esr').value,n)}
vm.runInContext('state={connected:true,config:params(),reading:{output:false}};controls()',context);
assert.equal(element('on').disabled,false);
element('preset-battery-1').onclick();assert.equal(element('on').disabled,true);
for(const blocked of ['state.reading.output=true','state.reading.output=false;state.debug_only=true','state.debug_only=false;working=true']){
 vm.runInContext(blocked+';controls()',context);
 const before=JSON.stringify(vm.runInContext('params()',context));
 element('preset-battery-4').onclick();element('preset-io-8').onclick();element('preset-esr-10').onclick();
 assert.equal(JSON.stringify(vm.runInContext('params()',context)),before);
 assert.equal(element('preset-battery-4').disabled,true);
}
vm.runInContext('working=false;state=null',context);
element('role').value='sink';element('mode').value='CR';vm.runInContext('adjust()',context);
const before=JSON.stringify(vm.runInContext('params()',context));
element('preset-battery-2').onclick();element('preset-io-4').onclick();element('preset-esr-0').onclick();
assert.equal(JSON.stringify(vm.runInContext('params()',context)),before);
assert.equal(element('preset-io-4').disabled,true);
console.log('PASS UI logic: default IP, ESR preview, CR fields, diagnostic interlocks, settings verification, lost connection, all presets, draft-only actions and output interlocks');
