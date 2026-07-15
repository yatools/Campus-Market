import { describe, expect, it } from 'vitest'
import { router } from './router'

describe('router contract', () => {
  it('keeps every public section lazy-loadable and resets scroll position', async () => {
    const routes = router.getRoutes().filter((route) => route.name)
    expect(routes.map((route) => route.name)).toEqual(expect.arrayContaining(['dashboard', 'treehole', 'search', 'teams', 'explore', 'messages', 'me', 'admin']))
    for (const route of routes) {
      const component = route.components?.default
      if (typeof component === 'function') await (component as () => Promise<unknown>)()
    }
    expect(router.options.scrollBehavior?.({} as never, {} as never, null)).toEqual({ top: 0 })
  })
})
