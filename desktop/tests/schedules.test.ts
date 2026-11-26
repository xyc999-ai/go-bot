import test from 'node:test'
import assert from 'node:assert/strict'
import { scheduleGroup, scheduleSession, scheduleTime, scheduleRule, type Schedule } from '../renderer/src/tasks/schedules.ts'
const task: Schedule = {record:{id:'task',title:'Review',prompt:'Review report',kind:'weekly',scheduledAt:'2026-11-23T01:00:00Z',time:'09:00:00.000',timeZone:'Asia/Shanghai',weekdays:[1,3,5]},status:'active',target:{kind:'session',sessionId:'original'}}
test('paused schedules remain upcoming; only ended schedules are completed',()=>{
 assert.equal(scheduleGroup(task),'upcoming')
 assert.equal(scheduleGroup({...task,status:'paused'}),'upcoming')
 assert.equal(scheduleGroup({...task,status:'ended'}),'completed')
})
test('delivery target opens fixed or last dynamically created conversation',()=>{
 assert.equal(scheduleSession(task),'original')
 assert.equal(scheduleSession({...task,target:{kind:'new'}}),undefined)
 assert.equal(scheduleSession({...task,target:{kind:'new'},lastDelivery:{scheduledAt:'',deliveredAt:'',messageId:'m',targetSessionId:'delivered'}}),'delivered')
})
test('task time uses its saved timezone and does not show zero-date as a real run',()=>{
 assert.match(scheduleTime(task.record.scheduledAt,'en','Asia/Shanghai'),/09:00/)
 assert.equal(scheduleTime(undefined,'en'),'—')
 assert.equal(scheduleTime('0001-01-01T00:00:00Z','en'),'—')
 assert.equal(scheduleTime('invalid','en'),'—')
 assert.equal(scheduleRule(task,'en',x=>x),'Weekly · Mon · Wed · Fri · 09:00')
})
