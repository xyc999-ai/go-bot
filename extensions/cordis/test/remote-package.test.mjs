import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, writeFile, readFile, readdir, access, rm, symlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { execFileSync } from 'node:child_process'
import http from 'node:http'
import { once } from 'node:events'
import { Context } from '@deepseek-ai/cordis'
import { Packages } from '../src/packages.mjs'

test('remote tarballs run approved postinstall and use the shared Host framework', async t => {
  const root=await mkdtemp(join(tmpdir(),'go-bot-remote-package-'))
  t.after(()=>rm(root,{recursive:true,force:true}))
  const directory=join(root,'source'),profile=join(root,'profile'),marker=join(root,'installed.txt')
  await mkdir(directory);await mkdir(profile)
  const name='fixture-remote'
  await writeFile(join(directory,'package.json'),JSON.stringify({name,version:'1.0.0',type:'module',exports:'./index.mjs',goBot:{api:'1'},
    peerDependencies:{'@deepseek-ai/cordis':'~4.1.0'},peerDependenciesMeta:{'@deepseek-ai/cordis':{optional:true}},
    scripts:{postinstall:`node -e 'require("node:fs").writeFileSync(${JSON.stringify(marker)},"installed")'`}}))
  await writeFile(join(directory,'index.mjs'),`export { Context } from '@deepseek-ai/cordis';export const inject=['host'];export function apply(){}`)
  const [packed]=JSON.parse(execFileSync('npm',['pack','--ignore-scripts','--json'],{cwd:directory,encoding:'utf8'}))
  const data=await readFile(join(directory,packed.filename))
  const server=http.createServer((_r,w)=>{w.setHeader('Content-Type','application/octet-stream');w.end(data)})
  server.listen(0,'127.0.0.1');await once(server,'listening')
  t.after(()=>{server.closeAllConnections();server.close()})
  const source={type:'tarball',spec:`http://127.0.0.1:${server.address().port}/fixture-remote-1.0.0.tgz`}
  const manager=new Packages({directory:profile})
  const inspected=await manager.inspect(name,source)
  assert.equal(inspected.version,'1.0.0')
  await assert.rejects(access(marker))
  // A persistent profile may still point to the preceding Host image's framework.
  await mkdir(join(profile,'node_modules','@deepseek-ai'),{recursive:true})
  await symlink(join(root,'retired-host-framework'),join(profile,'node_modules','@deepseek-ai','cordis'))
  await manager.install(name,source,{builds:[name]})
  assert.equal(await readFile(marker,'utf8'),'installed')
  const business=await import(pathToFileURL(join(profile,'node_modules',name,'index.mjs')).href)
  assert.equal(business.Context,Context)
  const dependencies=await readdir(join(profile,'node_modules','.pnpm'))
  assert.deepEqual(dependencies.filter(name=>/@deepseek-ai\+(?:cordis|cosmokit)|@standard-schema\+/.test(name)),[])
})
