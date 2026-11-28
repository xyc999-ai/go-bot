// Native UI regression against a production binary and an isolated local fixture.
// Requires Xcode, CocoaPods (xcodeproj Ruby gem), and a booted QA simulator.
const { execFileSync, spawn } = require('node:child_process')
const fs = require('node:fs'), path = require('node:path')
const root = path.resolve(__dirname, '../..'), qa = path.join(root, '.local/oauth-ios-qa')
const run = (command, args, options = {}) => execFileSync(command, args, { cwd: root, encoding: 'utf8', stdio: 'pipe', ...options })
async function main() {
 fs.mkdirSync(qa,{recursive:true})
 const devices=Object.values(JSON.parse(run('xcrun',['simctl','list','devices','booted','--json'])).devices).flat()
 const device=devices.find(d=>d.udid===process.env.OAUTH_IOS_DEVICE)||(!process.env.OAUTH_IOS_DEVICE&&devices[0])
 if(!device)throw Error('Boot a dedicated iOS QA simulator first.')
 const binary=process.env.OAUTH_IOS_APP||path.join(root,'.local/oauth-ios-build/Build/Products/Release-iphonesimulator/GoBot.app')
 if(!fs.existsSync(binary))throw Error('Set OAUTH_IOS_APP to the built Go Bot simulator .app.')
 let fixture
 try {
  try{if(!(await fetch('http://127.0.0.1:8094/__test/state')).ok)throw Error()}
  catch{fixture=spawn(process.execPath,[path.join(root,'desktop/scripts/oauth-loopback-fixture.cjs')],{stdio:'ignore'});for(let i=0;i<50;i++){try{if((await fetch('http://127.0.0.1:8094/__test/state')).ok)break}catch{};await new Promise(r=>setTimeout(r,100))}}
  run('xcrun',['simctl','install',device.udid,binary])
  const container=run('xcrun',['simctl','get_app_container',device.udid,'com.gobot.mobile','data']).trim(), documents=path.join(container,'Documents')
  fs.mkdirSync(documents,{recursive:true});fs.writeFileSync(path.join(documents,'controller-profile.json'),JSON.stringify({server:'http://127.0.0.1:8094',account:'oauth-fixture',autoConnect:true}));fs.writeFileSync(path.join(documents,'ui-language.txt'),'en')
  // Uninstall the previous test runner so Simulator does not reuse its loaded bundle.
  try{run('xcrun',['simctl','uninstall',device.udid,'com.gobot.oauth-uitests.xctrunner'])}catch{}
  const ruby=process.env.OAUTH_RUBY||['/opt/homebrew/opt/ruby/bin/ruby','/usr/local/opt/ruby/bin/ruby'].find(fs.existsSync)||'ruby'
  const gem=process.env.OAUTH_GEM_HOME||['/opt/homebrew/opt/cocoapods/libexec','/usr/local/opt/cocoapods/libexec'].find(fs.existsSync)||process.env.GEM_HOME
  const script=String.raw`require 'xcodeproj'
root=ENV.fetch('GOBOT_OAUTH_QA_ROOT'); qa=File.join(root,'.local/oauth-ios-qa')
p=Xcodeproj::Project.new(File.join(qa,'OAuthUITests.xcodeproj'));t=p.new_target(:ui_test_bundle,'OAuthUITests',:ios,'16.4')
f=p.main_group.new_file(File.join(root,'mobile/tests/native/OAuthLoopbackUITests.swift'));t.source_build_phase.add_file_reference(f)
t.build_configurations.each{|c|c.build_settings.merge!({'PRODUCT_BUNDLE_IDENTIFIER'=>'com.gobot.oauth-uitests','GENERATE_INFOPLIST_FILE'=>'YES','SWIFT_VERSION'=>'5.9','CODE_SIGNING_ALLOWED'=>'NO','TARGETED_DEVICE_FAMILY'=>'1','SDKROOT'=>'iphonesimulator'})};p.save
s=Xcodeproj::XCScheme.new;s.add_build_target(t);s.add_test_target(t);s.save_as(p.path,'OAuthUITests')`
  run(ruby,['-e',script],{env:{...process.env,...(gem?{GEM_HOME:gem}:{}),GOBOT_OAUTH_QA_ROOT:root}})
  const result=path.join(qa,'results-'+Date.now()),log=fs.openSync(path.join(qa,'last-test.log'),'w')
  try{run('xcodebuild',['-project',path.join(qa,'OAuthUITests.xcodeproj'),'-scheme','OAuthUITests','-destination','platform=iOS Simulator,id='+device.udid,'-derivedDataPath',path.join(qa,'build'),'-resultBundlePath',result,'CODE_SIGNING_ALLOWED=NO','test'],{stdio:['ignore',log,log],timeout:300000})}finally{fs.closeSync(log)}
  console.log('iOS OAuth callback, browser cancellation, fresh authorization and root-path callback passed. Results: '+result)
 }finally{try{run('xcrun',['simctl','terminate',device.udid,'com.gobot.mobile'])}catch{};fixture?.kill('SIGTERM')}
}
main().catch(error=>{console.error(error.message);process.exitCode=1})
