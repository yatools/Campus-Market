import { flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'
import type { Editor } from '@tiptap/core'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RichEditor from './RichEditor.vue'

const uploadImage = vi.fn()
vi.mock('../api', () => ({ uploadImage: (...args: unknown[]) => uploadImage(...args) }))

function pasteEvent(file: File): Event {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', {
    value: { files: [file], items: [], getData: () => '', setData: () => undefined },
  })
  return event
}

function editorOf(wrapper: VueWrapper): Editor {
  const editor = (wrapper.vm as unknown as { editor: Editor | null }).editor
  if (!editor) throw new Error('RichEditor was not initialized')
  return editor
}

describe('RichEditor', () => {
  beforeEach(() => {
    uploadImage.mockReset()
    uploadImage.mockResolvedValue({ id: 8, url: '/uploads/pasted.webp', thumbnail_url: '/uploads/pasted-thumb.webp' })
  })

  it('does not reset the caret when the parent echoes an internal markdown update', async () => {
    const wrapper = mount(RichEditor, { props: { modelValue: '开头', ariaLabel: '测试正文' } })
    const editor = editorOf(wrapper)
    expect(editor.view.dom.getAttribute('role')).toBe('textbox')
    expect(editor.view.dom.getAttribute('aria-label')).toBe('测试正文')
    expect(editor.view.dom.getAttribute('aria-multiline')).toBe('true')
    editor.chain().focus('end').insertContent('继续输入').run()
    await nextTick()
    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as string
    const selection = editor.state.selection.from
    await wrapper.setProps({ modelValue: emitted })
    expect(editor.state.selection.from).toBe(selection)
    expect(editor.getMarkdown()).toContain('继续输入')
    wrapper.unmount()
  })

  it('uploads a clipboard image and inserts it at the visual editor selection', async () => {
    const wrapper = mount(RichEditor, { attachTo: document.body, props: { modelValue: '', attachments: [] } })
    await nextTick()
    const editor = editorOf(wrapper)
    editor.view.dom.dispatchEvent(
      pasteEvent(new File(['image'], 'clipboard.webp', { type: 'image/webp' })),
    )
    await flushPromises()
    expect(uploadImage).toHaveBeenCalledOnce()
    expect(wrapper.emitted('update:attachments')?.[0]?.[0]).toEqual([
      expect.objectContaining({ id: 8, url: '/uploads/pasted.webp' }),
    ])
    expect(editor.getHTML()).toContain('src="/uploads/pasted.webp"')
    wrapper.unmount()
  })

  it('inserts pasted images as markdown in advanced mode', async () => {
    const wrapper = mount(RichEditor, { props: { modelValue: '正文', attachments: [] } })
    await wrapper.findAll('.editor-mode-tabs button')[1].trigger('click')
    wrapper.find('textarea').element.dispatchEvent(
      pasteEvent(new File(['image'], 'clipboard.png', { type: 'image/png' })),
    )
    await flushPromises()
    const value = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as string
    expect(value).toContain('![图片](/uploads/pasted.webp)')
    wrapper.unmount()
  })

  it('marks selected observe text for public redaction', async () => {
    const wrapper = mount(RichEditor, { props: { modelValue: '张三在现场', enableRedaction: true } })
    const editor = editorOf(wrapper)
    editor.commands.setTextSelection({ from: 1, to: 3 })
    await wrapper.find('[data-testid="redaction-button"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toContain('==张三==')
    expect(editor.getHTML()).toContain('data-redaction="true"')
    expect(wrapper.text()).toContain('去码查看')
    wrapper.unmount()
  })
})
