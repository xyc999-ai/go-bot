import { spawn } from 'node:child_process'
import { mkdir, mkdtemp, readFile, writeFile, rm, realpath, rename, symlink, lstat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { isAbsolute, join, relative, dirname } from 'node:path'
import semver from 'semver'
import { createRequire } from 'node:module'
import { packageName, readManifest } from './manifest.mjs'
import { RuntimeError } from './errors.mjs'


const hostSDKName = '@go-bot/plugin-sdk'
const sharedCordis = dirname(createRequire(import.meta.url).resolve('@deepseek-ai/cordis/package.json'))
const hostSDKPath = fileURLToPath(new URL('../../sdk/', import.meta.url))
const MAX_OUTPUT = 1 << 20
const allowedEnv = ['PATH','HOME','LANG','LC_ALL','TMPDIR','HTTP_PROXY','HTTPS_PROXY','NO_PROXY','http_proxy','https_proxy','no_proxy','SSL_CERT_FILE','SSL_CERT_DIR','NODE_EXTRA_CA_CERTS']
export function baseEnvironment() {
  return Object.fromEntries(allowedEnv.filter(name => process.env[name] !== undefined).map(name => [name,process.env[name]]))
}

// No shell, no user-supplied pnpm options, and no package-manager startup hooks.
// Output stays bounded and errors are redacted before leaving this process.
async function command(binary,args,{directory,environment={},signal,secrets=[]}={}) {
  return new Promise((accept,reject) => {
    const child=spawn(binary,args,{cwd:directory,env:{...baseEnvironment(),...environment},stdio:['ignore','pipe','pipe'],signal,killSignal:'SIGTERM'})
    const chunks=[];let length=0;let overflow=false
    const collect=chunk=>{length+=chunk.length;if(length>MAX_OUTPUT){overflow=true;child.kill('SIGKILL')}else chunks.push(chunk)}
    child.stdout.on('data',collect);child.stderr.on('data',collect)
    child.once('error',reject)
    child.once('close',code=>{
      let output=Buffer.concat(chunks).toString('utf8')
      for(const secret of secrets)if(secret)output=output.replaceAll(secret,'[redacted]')
      if(overflow)return reject(new RuntimeError('PACKAGE_OUTPUT_LIMIT','Package manager output exceeds 1 MiB'))
      if(code!==0)return reject(new RuntimeError('PACKAGE_COMMAND_FAILED',output.trim().slice(-6000)||`Package manager exited with code ${code}`))
      accept(output)
    })
  })
}

function safeURL(value,protocols=['https:']) {
  let url
  try{url=new URL(value)}catch{throw new RuntimeError('INVALID_SOURCE','Source URL is invalid')}
  if(!protocols.includes(url.protocol)||url.password||url.search||url.hash||(url.protocol!=='ssh:'&&url.username))throw new RuntimeError('INVALID_SOURCE','Source URLs cannot contain passwords, query strings or fragments')
  return url
}

export function validateSource(name,source,{fixed=false}={}) {
  if(!packageName.test(name)||name.length>214||!source||typeof source.spec!=='string'||source.spec.length>4096||/[\0\r\n]/.test(source.spec))throw new RuntimeError('INVALID_SOURCE','Enter a valid package name and source')
  if(source.registry)safeURL(source.registry,['http:','https:'])
  if(source.authRef && (typeof source.authRef!=='string'||source.authRef.length>256||/[\0\r\n]/.test(source.authRef)))throw new RuntimeError('INVALID_SOURCE','Invalid credential reference')
  switch(source.type){
    case 'registry':{
      if(source.spec!==name&&!source.spec.startsWith(name+'@'))throw new RuntimeError('INVALID_SOURCE','Registry source must name the selected package')
      if(fixed&&!semver.valid(source.spec.slice(name.length+1)))throw new RuntimeError('UNPINNED_SOURCE','Registry packages must use an exact version')
      if(source.spec!==name&&!source.spec.slice(name.length+1))throw new RuntimeError('INVALID_SOURCE','A version or tag is required after @')
      break
    }
    case 'directory':
      if(!isAbsolute(source.spec))throw new RuntimeError('INVALID_SOURCE','A development directory must be an absolute Host path')
      break
    case 'tarball':
      if(!isAbsolute(source.spec)){
        const url=safeURL(source.spec,['https:','http:'])
        if(url.protocol==='http:'&&!['localhost','127.0.0.1','[::1]','host.docker.internal'].includes(url.hostname))throw new RuntimeError('INVALID_SOURCE','Remote tarballs require HTTPS')
      }
      break
    case 'git':{
      const split=source.spec.lastIndexOf('#')
      if(split<0)throw new RuntimeError('UNPINNED_SOURCE','Git sources require a commit or ref after #')
      const repository=source.spec.slice(0,split).replace(/^git\+/,'')
      safeURL(repository,['https:','ssh:'])
      gitPackageRef(source.spec.slice(split+1), fixed)
      break
    }
    default:throw new RuntimeError('INVALID_SOURCE','Unsupported package source type')
  }
}

function packageDirectory(source) {
  return source.spec
}

async function atomic(path,value) {
  const temporary=path+'.writing'
  await writeFile(temporary,value,{mode:0o600})
  await rename(temporary,path)
}

async function manifest(directory) {
  try{return JSON.parse(await readFile(join(directory,'package.json'),'utf8'))}
  catch(error){if(error.code!=='ENOENT')throw error;return {name:'go-bot-host-profile',private:true,type:'module',dependencies:{}}}
}

function information(name,source,manifest) {
  if(manifest.goBot?.api!=='1')throw new RuntimeError('INCOMPATIBLE_PLUGIN','A plugin must declare goBot.api "1"')
  return {name,version:manifest.version??'',description:manifest.description??'',license:typeof manifest.license==='string'?manifest.license:'',
    source,api:manifest.goBot?.api??'',title:manifest.goBot?.title??'',configSchema:manifest.goBot?.configSchema}
}

export class Packages {
  constructor({directory,pnpm='pnpm',credentials={},signal}={}){this.directory=directory;this.pnpm=pnpm;this.credentials=credentials;this.signal=signal}

  async #authentication(source,run){
    const secret=source.authRef?this.credentials[source.authRef]:undefined
    if(source.authRef&&!secret)throw new RuntimeError('CREDENTIAL_REQUIRED',`Missing installation credential: ${source.authRef}`)
    if(secret && /[\r\n\0]/.test(secret))throw new RuntimeError('INVALID_CREDENTIAL','Installation credentials cannot contain control characters')
    const directory=await mkdtemp(join(tmpdir(),'go-bot-install-'))
    try{
      const environment={CI:'true',NPM_CONFIG_USERCONFIG:join(directory,'npmrc'),NPM_CONFIG_GLOBALCONFIG:join(directory,'globalrc')}
      await writeFile(environment.NPM_CONFIG_GLOBALCONFIG,'',{mode:0o600})
      let npmrc=''
      if(source.type==='registry'){
        const registry=source.registry||'https://registry.npmjs.org/'
        const url=safeURL(registry,['http:','https:'])
        if(!url.pathname.endsWith('/'))url.pathname+='/'
        npmrc=`registry=${url.href}\n`
        if(secret)npmrc+=`//${url.host}${url.pathname}:_authToken=${secret}\n`
      }
      await writeFile(environment.NPM_CONFIG_USERCONFIG,npmrc,{mode:0o600})
      if(source.type==='git'&&secret){
        const base=source.spec.slice(0,source.spec.lastIndexOf('#')).replace(/^git\+/,'')
        const url=safeURL(base,['https:','ssh:'])
        if(url.protocol!=='https:')throw new RuntimeError('INVALID_CREDENTIAL','Token authentication is only supported for HTTPS Git sources')
        const username=source.username||'git'
        if(/[\r\n\0:]/.test(username))throw new RuntimeError('INVALID_CREDENTIAL','Invalid Git username')
        environment.GIT_CONFIG_COUNT='1'
        environment.GIT_CONFIG_KEY_0=`http.${url.href}.extraHeader`
        environment.GIT_CONFIG_VALUE_0=`Authorization: Basic ${Buffer.from(username+':'+secret).toString('base64')}`
        environment.GIT_TERMINAL_PROMPT='0'
      }
      return await run(environment,secret?[secret,environment.GIT_CONFIG_VALUE_0].filter(Boolean):[])
    }finally{await rm(directory,{recursive:true,force:true})}
  }

  async #run(args,source,directory=this.directory){
    return this.#authentication(source,(environment,secrets)=>command(this.pnpm,args,{directory,environment,secrets,signal:this.signal}))
  }

  async resolve(name,source){
    validateSource(name,source)
    if(source.type==='registry'){
      const text=await this.#run(['view',source.spec,'version','--json'],source)
      const versions=JSON.parse(text)
      const version=typeof versions==='string'?versions:Array.isArray(versions)?versions.filter(v=>semver.valid(v)).sort(semver.rcompare)[0]:undefined
      if(!semver.valid(version))throw new RuntimeError('PACKAGE_NOT_FOUND','Package version could not be resolved')
      return {...source,spec:name+'@'+version,registry:source.registry||'https://registry.npmjs.org/'}
    }
    if(source.type==='git'){
      const split=source.spec.lastIndexOf('#')
      const base=source.spec.slice(0,split)
      let {ref,suffix}=gitPackageRef(source.spec.slice(split+1))
      if(!/^[a-f0-9]{40}$/i.test(ref)){
        const text=await this.#authentication(source,(environment,secrets)=>command('git',['ls-remote','--exit-code',base.replace(/^git\+/,''),ref,ref+'^{}'],{directory:this.directory,environment,secrets,signal:this.signal}))
        ref=text.trim().split('\n').at(-1)?.split(/\s/)[0]
        if(!/^[a-f0-9]{40}$/i.test(ref))throw new RuntimeError('PACKAGE_NOT_FOUND','Git ref could not be resolved to a commit')
      }
      return {...source,spec:base+'#'+ref.toLowerCase()+suffix}
    }
    return {...source,spec:source.type==='directory'?await realpath(source.spec):source.spec}
  }

  async inspect(name,source){
    const resolved=await this.resolve(name,source)
    if(resolved.type==='registry'){
      const metadata=JSON.parse(await this.#run(['view',resolved.spec,'--json'],resolved))
      if(metadata.name!==name)throw new RuntimeError('INVALID_PACKAGE','Package metadata name does not match')
      return information(name,resolved,metadata)
    }
    if(resolved.type==='directory'){
      const metadata=JSON.parse(await readFile(join(packageDirectory(resolved),'package.json'),'utf8'))
      if(metadata.name!==name)throw new RuntimeError('INVALID_PACKAGE','Directory package name does not match')
      return information(name,resolved,metadata)
    }
    const directory=await mkdtemp(join(tmpdir(),'go-bot-inspect-'))
    try{return await new Packages({directory,pnpm:this.pnpm,credentials:this.credentials,signal:this.signal}).install(name,resolved,{scripts:false})}
    finally{await rm(directory,{recursive:true,force:true})}
  }

  async #framework() {
    const link=join(this.directory,'node_modules','@deepseek-ai','cordis')
    await mkdir(dirname(link),{recursive:true})
    try {
      const info=await lstat(link)
      if (!info.isSymbolicLink()) throw new RuntimeError('FRAMEWORK_CONFLICT','Cordis is supplied by the Host; business packages must use a peer dependency')
      const target=await realpath(link).catch(error=>{if(error.code==='ENOENT')return null;throw error})
      if (target===await realpath(sharedCordis)) return
      await rm(link,{force:true})
    } catch(error) { if(error.code!=='ENOENT') throw error }
    await symlink(await realpath(sharedCordis),link,'dir')
  }

  async install(name,source,{scripts=true,builds=[]}={}){
    validateSource(name,source,{fixed:true})
    if(name===hostSDKName)throw new RuntimeError('INVALID_PACKAGE','The Host supplies its own plugin SDK')
    await mkdir(this.directory,{recursive:true,mode:0o700})
    const prior=await manifest(this.directory)
    prior.private=true;prior.type='module';prior.scripts={}
    // The SDK is shipped with the Host, not fetched from an unpublished npm
    // name. Root and transitive imports resolve the same supported API.
    const sdkSpec='file:'+hostSDKPath
    prior.dependencies={...(prior.dependencies??{}),[hostSDKName]:sdkSpec}
    prior.pnpm={...(prior.pnpm??{}),overrides:{...(prior.pnpm?.overrides??{}),[hostSDKName]:sdkSpec}}

    await atomic(join(this.directory,'package.json'),JSON.stringify(prior,null,2)+'\n')
    const previous=prior.goBotSources?.[name]
    if(prior.dependencies?.[name] && (source.type==='directory'||previous?.type!==source.type||previous?.spec!==source.spec||previous?.registry!==source.registry)){
      // pnpm may retain the old file dependency resolution while updating its
      // specifier. Remove that importer's old edge before resolving a new one.
      await this.#run(['--config.ignore-scripts=true','--config.ignore-pnpmfile=true','remove','--reporter=silent','--',name],source)
    }
    const local = source.type==='directory'
    const spec=source.type==='registry'?source.spec:local?name+'@file:'+packageDirectory(source):name+'@'+source.spec
    if (scripts) {
      // Use pnpm's native per-resolution script policy. The reviewed catalog
      // supplies build permissions; public clients cannot grant them.
      const text = await this.#run(['config','get','allowBuilds','--json'], source)
      const existing = text.trim() && text.trim() !== 'undefined' ? JSON.parse(text) : {}
      const allowBuilds = Object.fromEntries(Object.entries(existing ?? {}).filter(([, value]) => typeof value === 'boolean'))
      for (const build of builds) {
        if (build === name) {
          if (local) allowBuilds[name+'@file:'+relative(await realpath(this.directory),packageDirectory(source))] = true
          else if (source.type === 'registry') allowBuilds[source.spec] = true
          else if (source.type === 'tarball') allowBuilds[name+'@'+source.spec] = true
          else throw new RuntimeError('BUILD_NOT_APPROVED', 'Git script approvals require a reviewed exact pnpm resolution')
        } else {
          const split = build.lastIndexOf('@')
          if (split <= 0 || !packageName.test(build.slice(0,split)) || !semver.valid(build.slice(split+1))) throw new RuntimeError('BUILD_NOT_APPROVED','Dependency build permissions require exact package versions')
          allowBuilds[build] = true
        }
      }
      await atomic(join(this.directory,'pnpm-workspace.yaml'), JSON.stringify({packages:['.'],autoInstallPeers:false,strictDepBuilds:true,sideEffectsCache:false,allowBuilds},null,2)+'\n')
    }
    await this.#framework()
    await this.#run(['add','--save-exact','--ignore-pnpmfile',...scripts?[]:['--ignore-scripts'],'--reporter=append-only','--',spec],source)
    await this.#framework()
    const {manifest:installed}=await readManifest(this.directory,name,{checkCompatibility:true})
    const updated=await manifest(this.directory)
    updated.goBotSources={...(updated.goBotSources??{}),[name]:source}
    await atomic(join(this.directory,'package.json'),JSON.stringify(updated,null,2)+'\n')
    return {...information(name,source,installed),present:true}
  }

  async remove(name){
    if(!packageName.test(name)||name===hostSDKName)throw new RuntimeError('INVALID_PACKAGE','Invalid plugin package name')
    await this.#run(['--config.ignore-scripts=true','--config.ignore-pnpmfile=true','remove','--reporter=silent','--',name],{type:'registry',spec:name})
    return {name,removed:true}
  }

  async installed(){
    const data=await manifest(this.directory)
    const packages=[]
    for(const [name,spec]of Object.entries(data.dependencies??{})){
      if(name===hostSDKName)continue
      try{const {manifest:m}=await readManifest(this.directory,name,{checkCompatibility:true});packages.push({...information(name,data.goBotSources?.[name]??{type:'installed',spec},m),present:true})}
      catch{packages.push({name,present:false})}
    }
    return packages
  }

}

const pathSegment = /^[A-Za-z0-9._@+-]+$/
export function gitPackageRef(value, fixed = false) {
  const parts = value.split('&path:/')
  if (parts.length > 2 || !parts[0] || /[\s\0&]/.test(parts[0]) || parts[0].startsWith('-') ||
      (parts[1] !== undefined && parts[1].split('/').some(part => !pathSegment.test(part) || part === '.' || part === '..'))) {
    throw new RuntimeError('INVALID_SOURCE', 'Invalid Git ref or package directory')
  }
  if (fixed && !/^[a-f0-9]{40}$/i.test(parts[0])) throw new RuntimeError('UNPINNED_SOURCE', 'Git sources must use a full commit')
  return { ref: parts[0], suffix: parts.length === 2 ? '&path:/' + parts[1] : '' }
}
