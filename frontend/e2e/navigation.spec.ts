import { expect, test } from '@playwright/test'

const creditRuleSet = {
  max_score: 1000,
  initial_score: 800,
  values: {
    'threshold.anonymous_post': 600, 'threshold.team_create': 600, 'threshold.course_review': 600,
    'threshold.listing_publish': 700, 'threshold.contact_publish': 700, 'threshold.observe_publish': 750,
    'threshold.high_credit': 800, 'threshold.dm_unlimited': 850, 'reward.team_check_in': 2,
    'reward.lost_claim': 5, 'reward.feedback_accepted': 5, 'penalty.team_late_leave': -20,
  },
  rules: [],
}

test.beforeEach(async ({ page }) => {
  await page.route('**/app-config.json', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ api_prefix: '/api/v1', csrf_cookie_name: 'wutong_csrf' }) }))
  await page.route('**/api/v1/credit-rules', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify(creditRuleSet),
  }))
  await page.route('**/api/v1/market/options', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ categories: [{ id: 1, name: '数码', slug: 'digital' }], locations: [{ id: 1, name: '校内面交', slug: 'campus' }], conditions: [{ code: 'excellent', name: '九成新' }] }) }))
})

test('public navigation remains usable without a session', async ({ page }) => {
  const consoleErrors: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error' && !message.text().startsWith('Failed to load resource')) consoleErrors.push(message.text())
  })
  await page.route('**/api/v1/me', (route) => route.fulfill({
    status: 401,
    contentType: 'application/json',
    body: JSON.stringify({ code: 'AUTH_REQUIRED', message: '请先登录', field_errors: {}, request_id: 'e2e' }),
  }))
  for (const path of ['posts**', 'hot', 'teams**']) {
    await page.route(`**/api/v1/${path}`, (route) => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
    }))
  }
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
  }))
  await page.goto('/')
  await expect(page.getByText('梧桐墙', { exact: true }).first()).toBeVisible()
  await page.locator('a[href="/teams"]:visible').first().click()
  await expect(page.getByRole('heading', { name: '🎮 游戏车队大厅' })).toBeVisible()
  expect(consoleErrors).toEqual([])
})

test('campus email registration handles verification and submitting states', async ({ page }) => {
  await page.route('**/api/v1/me', (route) => route.fulfill({
    status: 401,
    contentType: 'application/json',
    body: JSON.stringify({ code: 'AUTH_REQUIRED', message: '请先登录', field_errors: {}, request_id: 'e2e' }),
  }))
  await page.route('**/api/v1/auth/request-code', (route) => route.fulfill({
    status: 202,
    contentType: 'application/json',
    body: JSON.stringify({ accepted: true, resend_after: 60 }),
  }))
  await page.route('**/api/v1/auth/register', (route) => route.fulfill({
    status: 201,
    contentType: 'application/json',
    body: JSON.stringify({ user: { id: 1, email: 'student@test.edu.cn', nickname: '梧桐同学' } }),
  }))
  await page.route('**/api/v1/posts**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ items: [], page: 1, page_size: 30, total: 0 }),
  }))
  await page.route('**/api/v1/hot', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
  }))
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
  }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
  }))

  await page.goto('/')
  await page.locator('.register-link:visible').click()
  await page.getByLabel('校园邮箱').fill('student@test.edu.cn')
  await page.getByLabel('昵称').fill('梧桐同学')
  await page.getByRole('button', { name: '发送验证码' }).click()
  await expect(page.getByText(/验证码已发送/)).toBeVisible()
  await page.getByLabel('验证码').fill('123456')
  await page.getByLabel('密码').fill('SafePassword123')
  await page.getByRole('checkbox').check()
  await page.getByRole('button', { name: '注册并自动登录' }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
})

test('signed-in user can publish a tree-hole post through the responsive UI', async ({ page }) => {
  const user = {
    id: 8, email: 'author@test.edu.cn', nickname: '发帖同学', alias: '梧桐#000008', campus_identity: 'student',
    role: 'user', status: 'active', credit: 800, xp: 0, avatar_url: null, dm_stranger_off: false, hide_online: false,
    unread_notifications: 0,
  }
  let created: Record<string, unknown> | null = null
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(user) }))
  await page.route('**/api/v1/notifications/stream', (route) => route.abort())
  await page.route('**/api/v1/hot', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
  }))
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
  }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }),
  }))
  await page.route('**/api/v1/posts**', async (route) => {
    if (route.request().method() === 'POST') {
      created = route.request().postDataJSON()
      await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ id: 88, ...created }) })
      return
    }
    const items = created ? [{
      id: 88, title: created.title, body: created.body, author: user.nickname, identity_mode: created.identity_mode,
      status: 'published', allow_comments: true, views: 0, likes: 0, favorites: 0, comments: 0,
      liked: false, favorited: false, mine: true, attachments: [], created_at: new Date().toISOString(), expires_at: null,
    }] : []
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items, page: 1, page_size: 30, total: items.length }) })
  })

  await page.goto('/treehole')
  await expect(page.locator('.treehole-composer .composer-prompt')).toBeVisible()
  await page.locator('.treehole-composer .composer-prompt').click()
  await page.getByLabel('标题（可选）').fill('E2E 发帖')
  await page.locator('.composer-options select').first().selectOption('nickname')
  await expect(page.locator('label .rich-editor')).toHaveCount(0)
  const visualEditor = page.getByRole('textbox', { name: '树洞正文' })
  await visualEditor.click()
  await expect(visualEditor).toBeFocused()
  await visualEditor.type('这是一条完整浏览器流程')
  await expect(visualEditor).toBeFocused()
  await visualEditor.type('产生的回归内容。')
  await expect(visualEditor).toBeFocused()
  await page.locator('.treehole-composer').getByRole('button', { name: '高级 Markdown', exact: true }).click()
  await page.locator('.treehole-composer .markdown-editor textarea').fill('这是一条完整浏览器流程产生的回归内容。\n\n**支持加粗**')
  await page.getByRole('button', { name: '发布', exact: true }).click()
  await expect(page.locator('.p-title').getByText('E2E 发帖', { exact: true })).toBeVisible()
  await expect(page.getByText('支持加粗')).toBeVisible()
  expect(created).toMatchObject({ identity_mode: 'nickname' })
})

test('V4 dashboard renders real announcements and global search results', async ({ page }) => {
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 7, title: '期末维护提醒', body: '周日凌晨短暂停服。', level: 'strong', audience: 'all', read: false, read_count: 0, published_at: new Date().toISOString() }], page: 1, page_size: 5, total: 1 }) }))
  await page.route('**/api/v1/hot', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 9, type: 'question', title: '北区哪里可以彩色胶装？', score: 18.5, likes: 7, favorites: 3, comments: 2 }], page: 1, page_size: 20, total: 1 }) }))
  await page.route('**/api/v1/posts**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 8, total: 0 }) }))
  await page.route('**/api/v1/feed**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(route.request().url().includes('/changes') ? { count: 0, watermark: new Date().toISOString() } : { items: [], page: 1, page_size: 20, total: 0, watermark: new Date().toISOString() }) }))
  await page.route('**/api/v1/search**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 9, type: 'question', title: '北区哪里可以彩色胶装？', summary: '二食堂旁边的打印店可以。' }], page: 1, page_size: 50, total: 1 }) }))

  await page.goto('/')
  await expect(page.getByRole('heading', { name: '今日公告栏' })).toBeVisible()
  await expect(page.locator('.notice-bar').getByText('期末维护提醒', { exact: true })).toBeVisible()
  await expect(page.getByRole('dialog', { name: '重要公告' })).toBeVisible()
  await page.getByRole('button', { name: '我已阅读 ✓' }).click()
  await expect(page.getByText('北区哪里可以彩色胶装？')).toBeVisible()
  await page.getByLabel('全站搜索').fill('胶装')
  await page.locator('.searchbox').getByRole('button', { name: '搜索' }).click()
  await expect(page.getByRole('heading', { name: '🔎 全站搜索' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '北区哪里可以彩色胶装？' })).toBeVisible()
})

test('mobile bottom navigation opens the complete V4 section drawer', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }))
  for (const path of ['posts**', 'hot', 'teams**', 'announcements**']) {
    await page.route(`**/api/v1/${path}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  }
  await page.route('**/api/v1/listings**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))

  await page.goto('/')
  await page.locator('.mobile-nav').getByRole('button', { name: /更多/ }).click()
  await expect(page.getByText('梧桐墙 · 全部板块')).toBeVisible()
  await page.locator('.mobile-drawer a[href="/explore/listings"]').click()
  await expect(page.getByRole('heading', { name: '🛒 二手集市' })).toBeVisible()
})

test('my-account tabs stay above every panel and fixed alias can be saved', async ({ page }) => {
  let alias = '梧桐旧马甲'
  const user = () => ({ id: 31, email: 'me@test.edu.cn', nickname: '后台同学', alias, campus_identity: 'student', role: 'user', status: 'active', credit: 800, xp: 10, avatar_url: null, dm_stranger_off: false, hide_online: false, unread_notifications: 0 })
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(user()) }))
  await page.route('**/api/v1/me/profile', async (route) => {
    alias = route.request().postDataJSON().alias
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(user()) })
  })
  await page.route('**/api/v1/me/privacy', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ dm_stranger_off: false, hide_online: false }) }))
  for (const path of ['me/favorites**', 'notifications**', 'me/sessions**', 'me/reports**', 'me/appeals**', 'me/content**']) {
    await page.route(`**/api/v1/${path}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  }
  await page.route('**/api/v1/notifications/stream', (route) => route.abort())
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))

  await page.goto('/me')
  const tabs = page.locator('.account-tabs-v4')
  const overview = page.locator('.credit-overview-v4')
  expect((await tabs.boundingBox())!.y).toBeLessThan((await overview.boundingBox())!.y)
  await tabs.getByRole('button', { name: '资料' }).click()
  const profile = page.getByRole('heading', { name: '资料设置' }).locator('..')
  expect((await tabs.boundingBox())!.y).toBeLessThan((await profile.boundingBox())!.y)
  await page.getByLabel('固定匿名昵称（马甲）').fill('月下小狐狸')
  await page.getByRole('button', { name: '保存资料' }).click()
  await expect(page.getByText('资料与隐私设置已保存')).toBeVisible()
  expect(alias).toBe('月下小狐狸')
})

test('V4 team and offline marketplace composers expose the completed fields', async ({ page }) => {
  const user = { id: 18, email: 'v4@test.edu.cn', nickname: 'V4同学', alias: '梧桐#18', campus_identity: 'student', role: 'user', status: 'active', credit: 820, xp: 20, avatar_url: null, dm_stranger_off: false, hide_online: false, unread_notifications: 0 }
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(user) }))
  await page.route('**/api/v1/notifications/stream', (route) => route.abort())
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/team-games', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 2, name: '无畏契约', aliases: ['瓦', 'Valorant'], active: true }], total: 1 }) }))
  await page.route('**/api/v1/listings**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))

  await page.goto('/teams')
  await page.getByRole('button', { name: '+ 发布开车' }).click()
  await expect(page.getByText('车队氛围')).toBeVisible()
  await expect(page.getByText('发车后保留时间')).toBeVisible()
  await expect(page.getByText('提醒方式（可多选）')).toBeVisible()
  await page.locator('input[type="datetime-local"]').fill('2020-01-01T12:00')
  await expect(page.getByText('发车时间已早于当前时间，请重新选择。')).toBeVisible()
  await expect(page.getByRole('button', { name: /QQ 群机器人 · 待接入/ })).toBeDisabled()
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('button', { name: '提交新游戏' }).click()
  await expect(page.getByRole('dialog', { name: '🕹️ 提交新游戏' })).toBeVisible()
  await page.getByRole('button', { name: '关闭', exact: true }).click()

  await page.goto('/explore/listings')
  await expect(page.locator('.userchip')).toBeVisible()
  await page.getByRole('button', { name: '+ 发布' }).click()
  await expect(page.getByRole('dialog', { name: '发布内容' })).toBeVisible()
  await expect(page.getByRole('dialog').getByText(/平台不经手资金/)).toBeVisible()
  await expect(page.getByText('中介担保')).toHaveCount(0)
})

test('V4 team tickets keep the original geometry and move management into details', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.clock.setFixedTime(new Date('2026-07-14T21:00:00+08:00'))
  const user = { id: 21, email: 'driver@test.edu.cn', nickname: '车头同学', alias: '梧桐#21', campus_identity: 'student', role: 'user', status: 'active', credit: 820, xp: 20, avatar_url: null, dm_stranger_off: false, hide_online: false, unread_notifications: 0 }
  const team = {
    id: 41, game: '无畏契约', game_id: 2, mode: '竞技排位', rank_requirement: '黄金~铂金', capacity: 5,
    member_count: 1, members: [{ id: 21, nickname: '车头同学', credit: 820 }],
    owner: { id: 21, nickname: '车头同学', credit: 820, verified: true }, completion_rate: 98,
    rating_tags: { friendly: 41, punctual: 38, communication: 27, skill: 19 }, voice_name: 'KOOK', voice_link: '',
    notes: '娱乐上分，不骂人。', newbie_level: '欢迎新手', vibe: '娱乐上分两不误', reminder_channels: ['email', 'in_app', 'calendar'],
    my_reminder_channels: ['email', 'in_app', 'calendar'], recurrence: 'once', reminder_minutes: 30, post_departure_retention_minutes: 120, status: 'active', joined: true, mine: true,
    next_run: { id: 4, starts_at: '2026-07-14T20:00:00', expires_at: '2026-07-14T22:00:00', status: 'scheduled' },
  }
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(user) }))
  await page.route('**/api/v1/notifications/stream', (route) => route.abort())
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [team], page: 1, page_size: 50, total: 1 }) }))
  await page.route('**/api/v1/team-games', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 2, name: '无畏契约', aliases: ['瓦'], active: true }], total: 1 }) }))

  await page.goto('/teams')
  const stub = page.locator('.ticket .t-right')
  const emptySeat = page.locator('.seatdots i.is-empty').first()
  await expect(stub).toBeVisible()
  expect(Math.round((await stub.boundingBox())!.width)).toBe(150)
  expect(Math.round((await emptySeat.boundingBox())!.width)).toBe(10)
  expect(await emptySeat.evaluate((node) => getComputedStyle(node).padding)).toBe('0px')
  expect(Math.round((await page.locator('.ticket').boundingBox())!.height)).toBeLessThan(145)
  await expect(page.locator('.ticket')).not.toContainText('编辑车队')
  if (testInfo.project.name === 'desktop-chromium' && process.platform === 'win32') await expect(page.locator('.team-page-v4')).toHaveScreenshot('team-page-v4.png', { animations: 'disabled', maxDiffPixelRatio: 0.005 })
  await page.getByRole('button', { name: '管理车队' }).click()
  await expect(page.getByRole('dialog')).toContainText('编辑车队')
  await expect(page.getByRole('dialog')).toContainText('娱乐上分，不骂人。')
})

test('V4 handbook opens on the twelve-category landing page before article drill-down', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/handbook**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 8, category: '新生入学指南', title: '报到当天完整流程', body: '先到学院报到，再领取宿舍钥匙。', featured: true, favorite_count: 12, mine: false, attachments: [] }], page: 1, page_size: 50, total: 1 }) }))
  await page.route('**/api/v1/campus-services', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 2, name: '文汇打印', category: '打印/维修/快递', score: 4.8, rating_count: 213, managed_by_me: false, next_rating_at: null }], total: 1 }) }))

  await page.goto('/explore/handbook')
  await expect(page.locator('.hb-item')).toHaveCount(12)
  await expect(page.getByRole('heading', { name: '🏅 贡献奖励体系' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '⭐ 校园服务评分（防恶意差评设计）' })).toBeVisible()
  await expect(page.getByText('报到当天完整流程')).toHaveCount(0)
  if (testInfo.project.name === 'desktop-chromium' && process.platform === 'win32') await expect(page.locator('.explore-page-v4')).toHaveScreenshot('handbook-landing-v4.png', { animations: 'disabled', maxDiffPixelRatio: 0.005 })
  await page.getByRole('button', { name: /新生入学指南/ }).click()
  await expect(page.getByText('报到当天完整流程')).toBeVisible()
  await expect(page.getByRole('button', { name: '← 返回分类' })).toBeVisible()
})

test('all remaining campus modules render their dedicated V4 structures', async ({ page }) => {
  const paged = (items: unknown[]) => JSON.stringify({ items, page: 1, page_size: 20, total: items.length })
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }))
  await page.route('**/api/v1/teams**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([]) }))
  await page.route('**/api/v1/questions/1', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ id: 1, title: '北区哪里可以彩色胶装？', body: '明早交材料。', category: '行政事务', tags: ['北校区'], bounty_xp: 50, accepted_answer_id: 2, answer_count: 1, mine: false, attachments: [], answers: [{ id: 2, author: '打印室之神', body: '二食堂西侧文汇打印。', attachments: [] }] }) }))
  await page.route('**/api/v1/questions**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([{ id: 1 }]) }))
  await page.route('**/api/v1/course-offerings**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([{ id: 2, course: '线性代数', teacher: '李老师', semester: '2026春', section: '1班', review_count: 37, score: 4.6, score_hidden_reason: null, tags: ['给分好', '板书清晰'], reviews: [] }]) }))
  await page.route('**/api/v1/activities**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([{ id: 3, title: '秋季纳新宣讲', author: '校辩论队', category: '社团招新', body: '欢迎报名。', location: '明德楼', starts_at: '2026-09-10T19:00:00', member_count: 4, capacity: 30, joined: false, mine: false, attachments: [] }]) }))
  await page.route('**/api/v1/lost-items**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([{ id: 4, kind: 'found', item_name: '白色降噪耳机', description: '已交至服务台。', location: '图书馆三楼', happened_at: '2026-07-14T14:20:00', created_at: '2026-07-14T14:30:00', status: 'open', mine: false, claim_count: 0, attachments: [] }]) }))
  await page.route('**/api/v1/observe-posts**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([{ id: 5, title: '自习教室外放短视频', body: '事件描述已打码。', response: '', admin_note: '', status: 'pending', respondent: false, created_at: '2026-07-14T12:00:00', attachments: [] }]) }))
  await page.route('**/api/v1/penalties**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([{ id: 6, user: '用户 A****7', violation_type: '发布违禁品', result: '删帖 · 信用 −10', rule: '规范 3.1', appeal_status: '', created_at: '2026-07-03T12:00:00' }]) }))
  await page.route('**/api/v1/announcements**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: paged([{ id: 7, title: '社区规范更新', body: '规则于本周生效。', level: 'strong', audience: 'all', read: false, read_count: 8241, published_at: '2026-07-01T12:00:00' }]) }))

  await page.goto('/explore/questions')
  await expect(page.locator('.qa-card-v4')).toBeVisible()
  await page.goto('/explore/courses')
  await expect(page.locator('.course-summary-v4')).toBeVisible()
  await page.goto('/explore/activities')
  await expect(page.locator('.activity-post-v4')).toBeVisible()
  await page.goto('/explore/lost')
  await expect(page.locator('.lost-post-v4')).toBeVisible()
  await page.goto('/explore/observe')
  await expect(page.locator('.observe-rules-v4')).toBeVisible()
  await expect(page.locator('.observe-post-v4')).toBeVisible()
  await page.goto('/explore/governance')
  await expect(page.locator('table.gov th').first()).toHaveText('匿名化账号')
  await page.goto('/explore/announcements')
  await expect(page.locator('.announcement-card-v4.strong')).toBeVisible()
})

test('buyer request, seller acceptance, bilateral confirmation and double-blind reviews form one market workflow', async ({ page }) => {
  const seller = { id: 10, email: 'seller@test.edu.cn', nickname: '卖家', alias: '梧桐#10', campus_identity: 'student', role: 'user', status: 'active', credit: 900, xp: 0, avatar_url: null, dm_stranger_off: false, hide_online: false, unread_notifications: 0 }
  const buyer = { ...seller, id: 20, email: 'buyer@test.edu.cn', nickname: '买家', alias: '梧桐#20' }
  let currentUser = buyer
  let status: 'requested' | 'reserved' | 'completed' = 'requested'
  let requested = false
  let buyerConfirmed = false
  let sellerConfirmed = false
  let reviews = 0
  const transaction = () => ({ id: 1, listing: { id: 5, title: '九成新显示器', price_cents: 50000 }, seller: { id: seller.id, nickname: seller.nickname }, buyer: { id: buyer.id, nickname: buyer.nickname }, status, message: '希望明天下午面交', reserved_until: status === 'reserved' ? new Date(Date.now() + 86400000).toISOString() : null, buyer_confirmed_at: buyerConfirmed ? new Date().toISOString() : null, seller_confirmed_at: sellerConfirmed ? new Date().toISOString() : null, completed_at: status === 'completed' ? new Date().toISOString() : null, dispute: null, created_at: new Date().toISOString(), updated_at: new Date().toISOString() })
  const listing = () => ({ id: 5, category: { id: 1, name: '数码', slug: 'digital' }, location: { id: 1, name: '校内面交', slug: 'campus' }, title: '九成新显示器', description: '功能正常，仅支持校内当面验货。', price_cents: 50000, condition: 'excellent', condition_label: '九成新', negotiable: true, purchased_at: null, trade_status: status === 'completed' ? 'completed' : status === 'reserved' ? 'reserved' : 'available', publication_status: 'published', moderation_status: 'approved', seller: { id: seller.id, nickname: seller.nickname, credit: 900, verified: true, completed_sales: status === 'completed' ? 1 : 0, rating_average: reviews === 2 ? 5 : 0, rating_count: reviews === 2 ? 2 : 0 }, mine: currentUser.id === seller.id, favorite_count: 0, attachments: [], created_at: new Date().toISOString(), updated_at: new Date().toISOString() })

  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(currentUser) }))
  await page.route('**/api/v1/notifications/stream', (route) => route.abort())
  for (const path of ['announcements**', 'teams**']) await page.route(`**/api/v1/${path}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], page: 1, page_size: 20, total: 0 }) }))
  await page.route('**/api/v1/listings**', async (route) => {
    if (route.request().method() === 'POST' && route.request().url().endsWith('/transactions')) {
      requested = true
      status = 'requested'
      await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify(transaction()) })
      return
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [listing()], page: 1, page_size: 20, total: 1 }) })
  })
  await page.route('**/api/v1/me/market-transactions**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: requested ? [transaction()] : [], page: 1, page_size: 20, total: requested ? 1 : 0 }) }))
  await page.route('**/api/v1/market-transactions/1/**', async (route) => {
    const url = route.request().url()
    if (url.endsWith('/accept')) status = 'reserved'
    if (url.endsWith('/confirm')) {
      if (currentUser.id === buyer.id) buyerConfirmed = true
      if (currentUser.id === seller.id) sellerConfirmed = true
      if (buyerConfirmed && sellerConfirmed) status = 'completed'
    }
    if (url.endsWith('/reviews')) reviews++
    await route.fulfill({ status: url.endsWith('/reviews') ? 201 : 200, contentType: 'application/json', body: JSON.stringify(transaction()) })
  })

  await page.goto('/explore/listings')
  page.once('dialog', (dialog) => dialog.accept('希望明天下午面交'))
  await page.locator('.market-card-v4').getByRole('button', { name: '申请预订' }).click()
  await expect(page.getByRole('dialog', { name: '我的买卖记录' })).toContainText('requested')

  currentUser = seller
  await page.reload()
  await page.getByRole('button', { name: '我的买卖' }).click()
  await page.getByRole('button', { name: '接受申请' }).click()
  await expect(page.getByRole('dialog', { name: '我的买卖记录' })).toContainText('reserved')
  await page.getByRole('button', { name: '我已完成面交' }).click()
  expect(sellerConfirmed).toBe(true)
  expect(status).toBe('reserved')

  currentUser = buyer
  await page.reload()
  await page.getByRole('button', { name: '我的买卖' }).click()
  await page.getByRole('button', { name: '我已完成面交' }).click()
  await expect(page.getByRole('dialog', { name: '我的买卖记录' })).toContainText('completed')
  let reviewAnswers = ['5', '物品与描述一致']
  const answerBuyerReview = (dialog: import('@playwright/test').Dialog) => dialog.accept(reviewAnswers.shift() || '')
  page.on('dialog', answerBuyerReview)
  await page.getByRole('button', { name: '提交双盲评价' }).click()
  page.off('dialog', answerBuyerReview)

  currentUser = seller
  await page.reload()
  await page.getByRole('button', { name: '我的买卖' }).click()
  reviewAnswers = ['5', '买家准时到场']
  const answerSellerReview = (dialog: import('@playwright/test').Dialog) => dialog.accept(reviewAnswers.shift() || '')
  page.on('dialog', answerSellerReview)
  await page.getByRole('button', { name: '提交双盲评价' }).click()
  page.off('dialog', answerSellerReview)
  expect(reviews).toBe(2)
})

test('private dispute evidence freezes a transaction until an administrator decides it', async ({ page }) => {
  const buyer = { id: 20, email: 'buyer@test.edu.cn', nickname: '买家', alias: '梧桐#20', campus_identity: 'student', role: 'user', status: 'active', credit: 900, xp: 0, avatar_url: null, dm_stranger_off: false, hide_online: false, unread_notifications: 0 }
  const admin = { ...buyer, id: 99, email: 'admin@test.edu.cn', nickname: '管理员', alias: '梧桐#99', role: 'admin' }
  const seller = { id: 10, nickname: '卖家' }
  let currentUser = buyer
  let transactionStatus = 'reserved'
  let evidenceUploaded = false
  let disputePending = false
  const transaction = () => ({ id: 1, listing: { id: 5, title: '九成新显示器', price_cents: 50000 }, seller, buyer: { id: buyer.id, nickname: buyer.nickname }, status: transactionStatus, message: '', reserved_until: new Date(Date.now() + 86400000).toISOString(), buyer_confirmed_at: null, seller_confirmed_at: null, completed_at: null, dispute: disputePending ? { id: 7, status: 'pending' } : null, created_at: new Date().toISOString(), updated_at: new Date().toISOString() })
  const emptyPage = { items: [], page: 1, page_size: 20, total: 0 }

  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(currentUser) }))
  await page.route('**/api/v1/notifications/stream', (route) => route.abort())
  for (const path of ['announcements**', 'teams**', 'listings**']) await page.route(`**/api/v1/${path}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(path.startsWith('listings') ? { ...emptyPage, items: [] } : emptyPage) }))
  await page.route('**/api/v1/me/market-transactions**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...emptyPage, items: [transaction()], total: 1 }) }))
  await page.route('**/api/v1/uploads/images', async (route) => {
    evidenceUploaded = (await route.request().postDataBuffer()) !== null
    await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ id: 31, content_url: '/api/v1/attachments/31/content' }) })
  })
  await page.route('**/api/v1/market-transactions/1/disputes', async (route) => {
    const body = route.request().postDataJSON()
    expect(body.attachment_ids).toEqual([31])
    disputePending = true
    transactionStatus = 'disputed'
    await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ id: 7, transaction_id: 1, status: 'pending' }) })
  })
  await page.route('**/api/v1/admin/**', (route) => {
    const url = route.request().url()
    if (url.endsWith('/overview') || url.endsWith('/settings') || url.endsWith('/system-health')) return route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    if (url.endsWith('/credit-rules')) return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(creditRuleSet) })
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(emptyPage) })
  })
  await page.route('**/api/v1/admin/market/disputes**', async (route) => {
    if (route.request().method() === 'POST') {
      disputePending = false
      transactionStatus = 'cancelled'
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ id: 7, transaction_id: 1, status: 'resolved', decision: 'cancelled' }) })
      return
    }
    const items = disputePending ? [{ id: 7, transaction_id: 1, opened_by: { id: buyer.id, nickname: buyer.nickname }, reason: '商品现场情况与描述明显不符', status: 'pending', evidence: [{ id: 31, content_url: '/api/v1/attachments/31/content' }], created_at: new Date().toISOString() }] : []
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...emptyPage, items, total: items.length }) })
  })
  for (const path of ['team-games', 'campus-services']) await page.route(`**/api/v1/${path}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }))

  await page.goto('/explore/listings')
  await page.getByRole('button', { name: '我的买卖' }).click()
  // Evidence inputs are per-transaction since the shared #market-evidence field caused
  // evidence to be attached to the wrong dispute.
  await page.locator('#market-evidence-1').setInputFiles({ name: 'evidence.png', mimeType: 'image/png', buffer: Buffer.from('89504e470d0a1a0a', 'hex') })
  page.once('dialog', (dialog) => dialog.accept('商品现场情况与描述明显不符'))
  await page.getByRole('button', { name: '发起纠纷' }).click()
  await expect.poll(() => evidenceUploaded).toBe(true)
  await expect.poll(() => transactionStatus).toBe('disputed')

  currentUser = admin
  await page.goto('/admin')
  await expect(page.getByText('交易 #1')).toBeVisible()
  page.once('dialog', (dialog) => dialog.accept('证据成立，取消本次交易'))
  await page.getByRole('button', { name: '裁定取消' }).click()
  await expect(page.getByText('交易纠纷已裁决并写入审计日志')).toBeVisible()
  await expect.poll(() => transactionStatus).toBe('cancelled')
})
