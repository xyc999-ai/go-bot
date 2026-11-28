const assert = require('node:assert/strict')
const http = require('node:http')
const {execFileSync} = require('node:child_process')
const fs = require('node:fs'), path = require('node:path')
const serial=process.env.OAUTH_ANDROID_SERIAL||'emulator-5580', root=path.resolve(__dirname,'../..'), out=path.join(root,'test-results/oauth')
const adbPath=path.join(process.env.HOME,'Library/Android/sdk/platform-tools/adb')
const adb=(...args)=>execFileSync(adbPath,['-s',serial,...args],{encoding:'utf8',timeout:30000})
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms))
function nodes(){
 const dump=adb('shell','uiautomator','dump','/sdcard/oauth-ui.xml')
 if(!dump.includes('UI hierchary dumped to:'))return []
 const xml=adb('shell','cat','/sdcard/oauth-ui.xml');fs.writeFileSync(path.join(out,'android-ui.xml'),xml)
 return [...xml.matchAll(/<node\b[^>]+>/g)].map(([tag])=>Object.fromEntries([...tag.matchAll(/([\w-]+)="([^"]*)"/g)].map(m=>[m[1],m[2].replace(/&amp;/g,'&').replace(/&quot;/g,'"')])))
}
const rect=node=>node.bounds.match(/\d+/g).map(Number)
function press(node){const b=rect(node);adb('shell','input','tap',String((b[0]+b[2])>>1),String((b[1]+b[3])>>1))}
async function find(labels,timeout=30000){
 const end=Date.now()+timeout
 while(Date.now()<end){
  const all=nodes()
  if(all.some(n=>n.text==="System UI isn't responding")){const button=all.find(n=>n.text==='Wait');if(button){press(button);await wait(1000);continue}}
  const permission=all.find(n=>n['resource-id']?.includes('permission_allow_button'))
  if(permission){press(permission);continue}
  const matches=all.filter(n=>labels.includes(n.text)||labels.includes(n['content-desc']))
  if(matches.length)return matches.find(n=>n.clickable==='true')||matches[0]
  await wait(300)
 }
 throw Error('Native control not found: '+labels.join(' / '))
}
async function tap(...labels){press(await find(labels));await wait(400)}
function screenshot(name){fs.writeFileSync(path.join(out,name+'.png'),execFileSync(adbPath,['-s',serial,'exec-out','screencap','-p']))}
async function state(){return (await fetch('http://127.0.0.1:8094/__test/state')).json()}
async function prepareBrowser(){
 // A fresh QA emulator has Chrome's first-run screens. Never add an account.
 for(let i=0;i<5;i++){
  const all=nodes(), control=all.find(n=>['Stay signed out','Use without an account','Not now','No thanks','Got it','Keep Google'].includes(n.text))
  if(!control){if(!all.length){await wait(500);continue}break}
  press(control);await wait(600)
 }
}
async function callback(){
 const config=(await state()).current.prompts[0].loopback, target=new URL(config.redirectUri)
 target.search=new URLSearchParams({code:'synthetic-android-code',state:config.state}).toString()
 // Inject the synthetic provider response while leaving the actual Custom Tab
 // in the foreground; opening a separate Chrome task would change Android BAL.
 const port=adb('forward','tcp:0','tcp:'+target.port).trim()
 try {
  const status=await new Promise((resolve,reject)=>http.get({hostname:'127.0.0.1',port:Number(port),path:target.pathname+target.search,headers:{Host:target.host}},reply=>{reply.resume();reply.on('end',()=>resolve(reply.statusCode))}).on('error',reject))
  assert.equal(status,200)
 }finally{adb('forward','--remove','tcp:'+port)}
}

async function run(){
 fs.mkdirSync(out,{recursive:true})
 // Run only on a dedicated QA emulator. Clearing this app is intentional.
 assert.ok(serial.startsWith('emulator-'),'use a QA emulator, never a personal phone')
 await fetch('http://127.0.0.1:8094/__test/reset')
 adb('shell','pm','clear','com.gobot.mobile')
 adb('shell','am','start','-n','com.gobot.mobile/.MainActivity')
 await find(['Connect','连接'])
 const fields=nodes().filter(n=>n.class==='android.widget.EditText')
 assert.equal(fields.length,2)
 press(fields[0]);adb('shell','input','text','http://10.0.2.2:8094')
 press(fields[1]);adb('shell','input','text','oauth-fixture')
 adb('shell','input','keyevent','4')
 await tap('Connect','连接')
 await tap('Me','我的')
 await tap('Plugin market','插件市场')
 await tap('OAuth receiver test')
 await tap('Accounts and authorization','账号与授权')
 await tap('Connect account')
 await tap('Continue in browser','在浏览器中继续')
 await wait(1000);await prepareBrowser();await callback()
 await find(['Authorization is ready.','授权已就绪。'])
 screenshot('android-authorized')
 await tap('Done','完成')
 await tap('Authorize again','重新授权')
 await tap('Continue in browser','在浏览器中继续')
 await tap('Close tab','关闭标签页','Close','关闭')
 await find(['Restart authorization','重新授权'])
 await fetch('http://127.0.0.1:8094/__test/root-path')
 await tap('Restart authorization','重新授权')
 await tap('Continue in browser','在浏览器中继续')
 await wait(700);await callback()
 await find(['Authorization is ready.','授权已就绪。'])
 screenshot('android-retry-authorized')
 assert.ok((await state()).answers.length>=2)
 console.log('Android production app → system browser → native loopback → automatic answer passed; browser cancellation and restart passed.')
}
run().catch(error=>{try{screenshot('android-error')}catch{};console.error(error);process.exitCode=1})
