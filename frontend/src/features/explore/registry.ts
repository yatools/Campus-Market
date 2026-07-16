export const exploreSections = ['questions', 'handbook', 'courses', 'listings', 'activities', 'lost', 'observe', 'governance', 'announcements'] as const
export type ExploreSection = (typeof exploreSections)[number]

export const exploreSectionInfo: Record<ExploreSection, { icon: string; title: string; subtitle: string }> = {
  questions: { icon: '🙋', title: '打听 · 求助', subtitle: '悬赏提问 · 采纳加经验 · 高赞回答可转入生存手册' },
  handbook: { icon: '📖', title: '生存手册', subtitle: '老登经验合集 · 积分与「被收藏 / 被采纳 / 加精 / 长期有效」绑定，拒绝灌水' },
  courses: { icon: '🎓', title: '课程评价', subtitle: '基于课程体验 · 同一课程同一学期限评一次 · 需达到信用门槛' },
  listings: { icon: '🛒', title: '二手集市', subtitle: '仅支持私信联系与校内线下面交 · 平台不经手资金' },
  activities: { icon: '🎪', title: '校园活动', subtitle: '组织与搭子的聚集地' },
  lost: { icon: '🧣', title: '失物招领', subtitle: '捡到/丢失 · 地点 · 时间 · 图片 · 认领状态' },
  observe: { icon: '🔍', title: '校园文明观察台', subtitle: '只描述事件，不曝光个人 · 涉及具体个人/组织必须先人工审核 · 发帖需达到信用门槛' },
  governance: { icon: '⚖️', title: '社区治理公示', subtitle: '处罚记录 · 规则判例库 · 违规案例说明（账号一律匿名化）' },
  announcements: { icon: '📢', title: '公告中心', subtitle: '规则更新 / 停服维护 / 处罚规范变更 = 强提醒' },
}

export const exploreEndpoints: Record<ExploreSection, string> = {
  questions: '/questions?page_size=50',
  handbook: '/handbook?page_size=50',
  courses: '/course-offerings',
  listings: '/listings?page_size=50',
  activities: '/activities',
  lost: '/lost-items',
  observe: '/observe-posts?page_size=50',
  governance: '/penalties',
  announcements: '/announcements',
}

export function isExploreSection(value: string): value is ExploreSection {
  return (exploreSections as readonly string[]).includes(value)
}
