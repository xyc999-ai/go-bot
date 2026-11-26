import { test } from 'node:test'
import assert from 'node:assert/strict'
import { sidebarBounds, clampSidebarWidth } from '../renderer/src/sessions/sidebarWidth.ts'

test('sidebar leaves room for chat and an open docked file panel', () => {
  assert.equal(sidebarBounds(1440, false).max, 480)
  assert.equal(sidebarBounds(1200, true).max, 336)
  assert.equal(sidebarBounds(1024, true).max, 480) // Files overlay at this width.
  assert.equal(sidebarBounds(768, false).max, 224)
  assert.equal(clampSidebarWidth(900, 336), 336)
  assert.equal(clampSidebarWidth(-100, 480), 220)
  assert.equal(clampSidebarWidth(NaN, 480), 264)
})
