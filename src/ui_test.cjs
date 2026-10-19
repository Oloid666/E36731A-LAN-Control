// Logic-only browser substitute. This is not visual/browser QA.
const vm=require('vm'),fs=require('fs'),assert=require('assert');
const defaults={role:'source',mode:'CV',ip:'169.254.30.1',setv:'3.7',seti:'.1',ovp:'4.3',setr:'100',esr:'10',minv:'3.5',maxv:'4.2',previewi:'1',timeout:'5000',pollms:'1000'};
const nodes={};const noop=()=>{};
function element(id){return nodes[id]??={value:defaults[id]??'',textContent:'',classList:{toggle:noop},disabled:false,addEventListener:noop,clientWidth:600,clientHeight:240,options:['CV','CC','CR','ESR'].map(value=>({value,disabled:false})),getContext:()=>new Proxy({},{get:()=>noop})}}
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
console.log('PASS UI logic: default IP, ESR preview, CR fields, diagnostic interlocks, settings verification, lost connection');
