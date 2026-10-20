// Browser workflow test with simulated API; no instrument connection.
const {chromium}=require(process.env.E36731A_PLAYWRIGHT_MODULE||'playwright');
const fs=require('fs'),assert=require('assert');
(async()=>{
 const browser=await chromium.launch({headless:true,...(process.env.E36731A_CHROMIUM?{executablePath:process.env.E36731A_CHROMIUM}:{}),args:['--no-sandbox']});
 const page=await browser.newPage({viewport:{width:1280,height:1000}}),errors=[],actions=[];
 page.on('pageerror',e=>errors.push(e.message));
 const config={role:'battery',mode:'CV',voltage:3.7,current:.1,ovp:4.3,resistance:100,esr_milliohm:0,min_voltage:3.5,max_voltage:4.2};
 let connected=false,applied=null,output=false,mode='PSUP',busy=false,releasePrepare,slow=true;
 function state(){return {version:'v0.5.0',connected,config:applied,reading:connected?{voltage:output?(applied?.voltage||0):0,current:0,power:0,output,actual:output?'CV':'OFF',emulation:mode,sample_ms:500,time:'TEST'}:null,history:[],logs:['TEST: browser UI with simulated API'],message:connected?'已連線':'未連線',identity:'E36731A · 瀏覽器測試替身',options:{timeout_ms:5000,poll_ms:1000},preferences:{ip:'169.254.30.1',config,options:{timeout_ms:5000,poll_ms:1000}},busy,operation:busy?'一鍵套用：等待儀器讀回':'',last_command:busy?'*OPC?':''}}
 await page.route('http://127.0.0.1:18765/**',async route=>{
  const req=route.request(),url=new URL(req.url());
  if(url.pathname==='/'){await route.fulfill({contentType:'text/html',body:fs.readFileSync(require('path').join(__dirname,'index.html'),'utf8')});return}
  if(url.pathname==='/api/heartbeat'){await route.fulfill({status:204});return}
  if(url.pathname==='/api/state'){await route.fulfill({json:state()});return}
  const sent=req.postDataJSON();actions.push(sent.action);
  if(sent.action==='connect')connected=true;
  if(sent.action==='prepare'){
   busy=true;
   if(slow){await new Promise(resolve=>releasePrepare=resolve);slow=false;await route.fulfill({json:{message:'已取消，保持連線',cancelled:true}});return}
   applied=sent.config;mode='PS2Q';output=false;busy=false;
  }
  if(sent.action==='off'){if(releasePrepare){releasePrepare();releasePrepare=null;applied=null}output=false;busy=false}
  if(sent.action==='on')output=true;
  await route.fulfill({json:{message:'操作完成'}});
 });
 await page.goto('http://127.0.0.1:18765/#test-token');
 await page.waitForFunction(()=>document.getElementById('connect').textContent==='已連線');
 assert.deepEqual(actions,['connect']);assert.equal(await page.locator('#role').inputValue(),'battery');
 await page.locator('#preset-battery-3').click();await page.locator('#preset-io-1').click();await page.locator('#apply').click();
 await page.waitForFunction(()=>document.getElementById('apply').disabled);
 assert.equal(await page.locator('#preset-battery-2').isEnabled(),true);assert.equal(await page.locator('#setv').isEditable(),true);
 await page.locator('#vup').click();assert.equal(await page.locator('#setv').inputValue(),'11.200');
 await page.locator('#off').click();await page.waitForFunction(()=>!document.getElementById('apply').disabled);
 assert.equal(connected,true);assert.equal(output,false);
 await page.locator('#preset-battery-3').click();await page.locator('#apply').click();
 await page.waitForFunction(()=>!document.getElementById('on').disabled);
 assert.equal(mode,'PS2Q');assert.equal(output,false);
 await page.locator('#on').click();await page.waitForFunction(()=>document.getElementById('output').textContent.includes('ON'));
 await page.locator('#off').click();await page.waitForFunction(()=>!document.getElementById('on').disabled);
 await page.screenshot({path:require('path').join(process.env.E36731A_SCREENSHOT_DIR||'/tmp','e36731a-lan-preview-v0.5.0.png'),fullPage:true});
 for(const width of [1280,720,390]){await page.setViewportSize({width,height:1000});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`horizontal overflow at ${width}`)}
 assert.deepEqual(errors,[]);console.log('PASS Chromium: default battery, exactly one auto-connect, prepare/stop/retry, edit draft while busy, PS2Q mode, OFF then ON enabled, desktop/mobile layouts; simulated API only');
 await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
