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
})

