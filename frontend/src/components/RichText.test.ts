import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import RichText from './RichText.vue'

describe('RichText', () => {
  it('removes executable HTML while keeping markdown', () => {
    const wrapper = mount(RichText, { props: { content: '**安全内容** <img src=x onerror=alert(1)><script>alert(1)</script>' } })
    expect(wrapper.html()).toContain('<strong>安全内容</strong>')
    expect(wrapper.html()).not.toContain('onerror')
    expect(wrapper.html()).not.toContain('<script')
  })

  it('renders uploaded inline images but removes external tracking images', () => {
    const wrapper = mount(RichText, { props: { content: '![校内图片](/uploads/2026/photo.webp)\n\n![外部图片](https://tracker.example/pixel.png)' } })
    expect(wrapper.find('img').attributes('src')).toBe('/uploads/2026/photo.webp')
    expect(wrapper.html()).toContain('loading="lazy"')
    expect(wrapper.html()).not.toContain('tracker.example')
  })

  it('strips dangerous link protocols and hardens outbound anchors', () => {
    const wrapper = mount(RichText, { props: { content: '[点我](javascript:alert(1)) [外链](https://example.edu.cn/a)' } })
    expect(wrapper.html()).not.toContain('javascript:')
    // Every surviving link must be non-referring and non-endorsing.
    expect(wrapper.html()).toContain('rel="noopener noreferrer nofollow"')
    expect(wrapper.html()).toContain('target="_blank"')
  })
})
