import fs from 'node:fs';
const base=process.argv[2];
if(!base)throw Error('Usage: node docs/ui-check.mjs http://127.0.0.1:<UI-port>');
const tabs=await (await fetch('http://127.0.0.1:19222/json')).json();
const ws=new WebSocket(tabs[0].webSocketDebuggerUrl);await new Promise(r=>ws.onopen=r);
let id=0;const pending=new Map();const failures=[];ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.id){pending.get(m.id)?.(m);pending.delete(m.id);}if(m.method==='Network.responseReceived'&&m.params.response.status>=400)failures.push(m.params.response.status);if(m.method==='Runtime.exceptionThrown')failures.push(m.params.exceptionDetails.text);};
function call(method,params={}){return new Promise((resolve,reject)=>{const n=++id;pending.set(n,m=>m.error?reject(m.error):resolve(m.result));ws.send(JSON.stringify({id:n,method,params}));});}
async function evaluate(expression){const r=await call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(JSON.stringify(r.exceptionDetails));return r.result.value;}
await call('Network.enable');await call('Runtime.enable');await call('Page.enable');await call('Emulation.setDeviceMetricsOverride',{width:1200,height:900,deviceScaleFactor:1,mobile:false});await call('Page.navigate',{url:base});await new Promise(r=>setTimeout(r,1800));
console.log('DOM',await evaluate(`JSON.stringify({nav:document.querySelectorAll('nav button').length,icons:document.querySelectorAll('nav svg').length,statusCard:!!document.querySelector('#statusCard'),bad:/404|\\ufffd|□/.test(document.querySelector('main').innerText),padding:getComputedStyle(document.querySelector('main')).padding,font:getComputedStyle(document.documentElement).fontSize})`));
if(!await evaluate(`document.querySelectorAll('nav button').length===5 && document.querySelectorAll('nav svg').length===5 && !!document.querySelector('#statusCard') && !/404|\\ufffd|□/.test(document.querySelector('main').innerText)`))throw Error('DOM assertion');
await evaluate(`document.querySelector('[data-page="Settings"]').click()`);await new Promise(r=>setTimeout(r,300));await evaluate(`document.querySelector('#language').value='en';document.querySelector('#language').dispatchEvent(new Event('change'));`);
console.log('i18n',await evaluate(`JSON.stringify({lang:document.documentElement.lang,nav:[...document.querySelectorAll('nav button')].map(b=>b.textContent),title:document.querySelector('#title').textContent})`));
if(!await evaluate(`document.documentElement.lang==='en'&&document.querySelector('nav button').textContent==='Dashboard'`))throw Error('i18n assertion');
await evaluate(`document.querySelector('#settings').requestSubmit()`);await new Promise(r=>setTimeout(r,400));
await call('Page.reload');await new Promise(r=>setTimeout(r,1000));if(!await evaluate(`document.documentElement.lang==='en'`))throw Error('persist language');
await evaluate(`document.querySelector('[data-page="Profiles"]').click()`);await new Promise(r=>setTimeout(r,300));console.log('profiles',await evaluate(`document.querySelectorAll('.profileRow').length`));
await evaluate(`document.querySelector('[data-page="Dashboard"]').click()`);await new Promise(r=>setTimeout(r,400));
const img=await call('Page.captureScreenshot',{format:'png'});fs.writeFileSync('docs/screenshots/dashboard.png',Buffer.from(img.data,'base64'));
console.log('network/browser errors',failures);if(failures.length)throw Error('browser/network errors');console.log('UI ASSERTIONS PASS');ws.close();
