import test from 'node:test'
import assert from 'node:assert/strict'
import { changeProtocolURL, modelProtocol, modelRequestURL } from '../renderer/src/settings/modelProtocol.ts'

test('protocol switch preserves custom URLs and replaces untouched defaults', () => {
  assert.equal(modelProtocol(), 'responses')
  assert.equal(changeProtocolURL('https://api.openai.com/v1', 'responses', 'anthropic_messages'), 'https://api.anthropic.com/v1')
  assert.equal(changeProtocolURL('https://proxy.example/prefix/v1', 'responses', 'anthropic_messages'), 'https://proxy.example/prefix/v1')
  assert.equal(modelRequestURL('https://proxy.example/apps/anthropic/v1/', 'anthropic_messages'), 'https://proxy.example/apps/anthropic/v1/messages')
})
