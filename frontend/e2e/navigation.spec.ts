import { expect, test } from '@playwright/test'

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
  await page.goto('/')
  await expect(page.getByText('梧桐墙', { exact: true }).first()).toBeVisible()
  await page.getByRole('link', { name: '车队' }).first().click()
  await expect(page.getByRole('heading', { name: '游戏车队' })).toBeVisible()
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

  await page.goto('/')
  await page.getByRole('button', { name: '校邮登录' }).click()
  await page.getByRole('button', { name: '注册' }).click()
  await page.getByLabel('校园邮箱').fill('student@test.edu.cn')
  await page.getByLabel('昵称').fill('梧桐同学')
  await page.getByRole('button', { name: '发送验证码' }).click()
  await expect(page.getByText(/验证码已发送/)).toBeVisible()
  await page.getByLabel('验证码').fill('123456')
  await page.getByLabel('密码').fill('SafePassword123')
  await page.getByRole('checkbox').check()
  await page.getByRole('button', { name: '验证并注册' }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
})

test('signed-in user can publish a tree-hole post through the responsive UI', async ({ page }) => {
  const user = {
    id: 8, email: 'author@test.edu.cn', nickname: '发帖同学', alias: '梧桐#000008', campus_identity: 'student',
    role: 'user', status: 'active', credit: 80, xp: 0, avatar_url: null, dm_stranger_off: false, hide_online: false,
    unread_notifications: 0,
  }
  let created: Record<string, unknown> | null = null
  await page.route('**/api/v1/me', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(user) }))
  await page.route('**/api/v1/notifications/stream', (route) => route.abort())
  await page.route('**/api/v1/hot', (route) => route.fulfill({
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

  await page.goto('/')
  await page.getByRole('button', { name: '写点什么' }).click()
  await page.getByLabel('标题（可选）').fill('E2E 发帖')
  await page.getByLabel('展示身份').selectOption('nickname')
  await page.getByLabel('正文').fill('这是一条完整浏览器流程产生的回归内容。')
  await page.getByRole('button', { name: '发布', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'E2E 发帖' })).toBeVisible()
  expect(created).toMatchObject({ identity_mode: 'nickname' })
})
