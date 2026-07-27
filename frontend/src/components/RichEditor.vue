<script setup lang="ts">
import Image from '@tiptap/extension-image'
import Link from '@tiptap/extension-link'
import Placeholder from '@tiptap/extension-placeholder'
import { Markdown } from '@tiptap/markdown'
import StarterKit from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/vue-3'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { uploadImage } from '../api'
import type { Attachment } from '../types'
import { maskRedactedMarkdown, Redaction } from './redaction'
import RichText from './RichText.vue'

const props = withDefaults(defineProps<{
  modelValue: string
  attachments?: Attachment[]
  placeholder?: string
  ariaLabel?: string
  maxImages?: number
  maxLength?: number
  enableRedaction?: boolean
}>(), { modelValue: '', attachments: () => [], placeholder: '写下具体内容…', ariaLabel: '正文编辑器', maxImages: 9, maxLength: 10000, enableRedaction: false })
const emit = defineEmits<{
  'update:modelValue': [value: string]
  'update:attachments': [value: Attachment[]]
}>()

const advanced = ref(false)
const markdown = ref(props.modelValue)
const rawInput = ref<HTMLTextAreaElement | null>(null)
const emojiOpen = ref(false)
const busy = ref(false)
const error = ref('')
const lastEmitted = ref(props.modelValue)
const previewMarkdown = computed(() => props.enableRedaction ? maskRedactedMarkdown(markdown.value) : markdown.value)
const emojis = ['😀', '😂', '🥹', '😍', '😎', '🤔', '😭', '😤', '👍', '👏', '🎉', '❤️', '🌳', '📌', '🚗', '🎮']
const allowedImageTypes = new Set(['image/jpeg', 'image/png', 'image/webp'])

const editor = useEditor({
  extensions: [
    StarterKit.configure({ link: false }),
    Link.configure({ openOnClick: false, autolink: true, linkOnPaste: true, defaultProtocol: 'https' }),
    Image.configure({ allowBase64: false, inline: false }),
    Placeholder.configure({ placeholder: props.placeholder }),
    Markdown,
    ...(props.enableRedaction ? [Redaction] : []),
  ],
  content: props.modelValue || { type: 'doc', content: [{ type: 'paragraph' }] },
  contentType: props.modelValue ? 'markdown' : 'json',
  onUpdate: ({ editor: instance }) => {
    const value = instance.getMarkdown()
    markdown.value = value
    lastEmitted.value = value
    emit('update:modelValue', value)
  },
  editorProps: {
    attributes: {
      role: 'textbox',
      'aria-label': props.ariaLabel,
      'aria-multiline': 'true',
    },
    handlePaste: (view, event) => {
      const files = clipboardImages(event)
      if (!files.length) return false
      event.preventDefault()
      const position = view.state.selection.from
      void uploadFiles(files, (uploaded) => insertVisualImages(uploaded, position))
      return true
    },
  },
})

watch(() => props.modelValue, (value) => {
  if (value === lastEmitted.value) return
  lastEmitted.value = value
  if (advanced.value) {
    markdown.value = value
    return
  }
  if (editor.value && value !== editor.value.getMarkdown()) {
    editor.value.commands.setContent(value, { contentType: 'markdown', emitUpdate: false })
  }
})

function setAdvanced(value: boolean) {
  if (value === advanced.value) return
  if (value) markdown.value = editor.value?.getMarkdown() || props.modelValue
  else editor.value?.commands.setContent(markdown.value, { contentType: 'markdown', emitUpdate: false })
  advanced.value = value
}

function rawChanged() {
  lastEmitted.value = markdown.value
  emit('update:modelValue', markdown.value)
}

function insertRaw(value: string) {
  const el = rawInput.value
  if (!el) { markdown.value += value; rawChanged(); return }
  const start = el.selectionStart, end = el.selectionEnd
  markdown.value = `${markdown.value.slice(0, start)}${value}${markdown.value.slice(end)}`
  rawChanged()
  void nextTick(() => { el.focus(); el.setSelectionRange(start + value.length, start + value.length) })
}

function insertEmoji(value: string) {
  if (advanced.value) insertRaw(value)
  else editor.value?.chain().focus().insertContent(value).run()
  emojiOpen.value = false
}

function toggleRedaction() {
  error.value = ''
  if (advanced.value) {
    const el = rawInput.value
    if (!el || el.selectionStart === el.selectionEnd) {
      error.value = '请先选中需要打码的文字'
      return
    }
    const start = el.selectionStart
    const end = el.selectionEnd
    const selected = markdown.value.slice(start, end)
    const alreadyMarked = selected.startsWith('==') && selected.endsWith('==') && selected.length > 4
    const replacement = alreadyMarked ? selected.slice(2, -2) : `==${selected}==`
    markdown.value = `${markdown.value.slice(0, start)}${replacement}${markdown.value.slice(end)}`
    rawChanged()
    void nextTick(() => {
      el.focus()
      el.setSelectionRange(start, start + replacement.length)
    })
    return
  }
  const instance = editor.value
  if (!instance) return
  if (instance.state.selection.empty) {
    if (instance.isActive('redaction')) {
      instance.chain().focus().extendMarkRange('redaction').toggleRedaction().run()
    } else {
      error.value = '请先选中需要打码的文字'
    }
    return
  }
  instance.chain().focus().toggleRedaction().run()
}

function addLink() {
  const href = window.prompt('输入链接地址（https://…）：', 'https://')?.trim()
  if (!href) return
  // Reject anything that is not plain http(s) at the point of entry. RichText's DOMPurify
  // pass already refuses to render javascript:/data: URLs, but relying on the renderer
  // alone means the dangerous string still gets stored and shipped to every other client.
  if (!/^https?:\/\/\S+$/i.test(href)) {
    window.alert('链接必须以 http:// 或 https:// 开头。')
    return
  }
  if (advanced.value) insertRaw(`[链接文字](${href})`)
  else editor.value?.chain().focus().extendMarkRange('link').setLink({ href }).run()
}

function clipboardImages(event: ClipboardEvent): File[] {
  const files = [...(event.clipboardData?.files || [])]
  if (files.length) return files.filter((file) => file.type.startsWith('image/'))
  return [...(event.clipboardData?.items || [])]
    .filter((item) => item.kind === 'file' && item.type.startsWith('image/'))
    .map((item) => item.getAsFile())
    .filter((file): file is File => Boolean(file))
}

function insertVisualImages(uploaded: Attachment[], position?: number) {
  if (!editor.value || !uploaded.length) return
  const content = uploaded.flatMap((item) => [
    { type: 'image', attrs: { src: item.url, alt: '图片', title: '帖子图片' } },
    { type: 'paragraph' },
  ])
  if (position == null) editor.value.chain().focus().insertContent(content).run()
  else editor.value.chain().focus().insertContentAt(Math.min(position, editor.value.state.doc.content.size), content).run()
}

async function uploadFiles(files: File[], insert: (uploaded: Attachment[]) => void) {
  if (busy.value) return
  const remaining = Math.max(0, props.maxImages - props.attachments.length)
  if (!remaining) { error.value = `最多只能插入 ${props.maxImages} 张图片`; return }
  const supported = files.filter((file) => allowedImageTypes.has(file.type))
  const selected = supported.slice(0, remaining)
  if (!selected.length) { error.value = '仅支持 JPEG、PNG 和 WebP 图片'; return }
  busy.value = true; error.value = ''
  const uploaded: Attachment[] = []
  let failures = 0
  for (const file of selected) {
    try { uploaded.push(await uploadImage(file)) }
    catch { failures += 1 }
  }
  if (uploaded.length) {
    emit('update:attachments', [...props.attachments, ...uploaded])
    insert(uploaded)
  }
  const omitted = files.length - selected.length
  if (failures || omitted || supported.length !== files.length) {
    error.value = [
      failures ? `${failures} 张图片上传失败` : '',
      omitted ? `另有 ${omitted} 张图片因格式或数量限制未插入` : '',
    ].filter(Boolean).join('；') || '部分图片未能插入'
  }
  busy.value = false
}

async function addImages(event: Event) {
  const input = event.target as HTMLInputElement
  const files = [...(input.files || [])]
  if (!files.length) { input.value = ''; return }
  if (advanced.value) {
    const position = rawInput.value?.selectionStart ?? markdown.value.length
    await uploadFiles(files, (uploaded) => {
      const syntax = uploaded.map((item) => `\n![图片](${item.url})\n`).join('')
      const before = markdown.value.slice(0, position)
      const after = markdown.value.slice(position)
      markdown.value = `${before}${syntax}${after}`
      rawChanged()
      void nextTick(() => { rawInput.value?.focus(); rawInput.value?.setSelectionRange(position + syntax.length, position + syntax.length) })
    })
  } else {
    const position = editor.value?.state.selection.from
    await uploadFiles(files, (uploaded) => insertVisualImages(uploaded, position))
  }
  input.value = ''
}

function pasteRaw(event: ClipboardEvent) {
  const files = clipboardImages(event)
  if (!files.length) return
  event.preventDefault()
  const position = rawInput.value?.selectionStart ?? markdown.value.length
  void uploadFiles(files, (uploaded) => {
    const syntax = uploaded.map((item) => `\n![图片](${item.url})\n`).join('')
    markdown.value = `${markdown.value.slice(0, position)}${syntax}${markdown.value.slice(position)}`
    rawChanged()
    void nextTick(() => { rawInput.value?.focus(); rawInput.value?.setSelectionRange(position + syntax.length, position + syntax.length) })
  })
}

onBeforeUnmount(() => editor.value?.destroy())
defineExpose({ editor })
</script>

<template>
  <div class="rich-editor" :class="{ advanced }">
    <div class="editor-mode-tabs"><button type="button" :class="{ active: !advanced }" @click="setAdvanced(false)">普通编辑</button><button type="button" :class="{ active: advanced }" @click="setAdvanced(true)">高级 Markdown</button></div>
    <div class="editor-toolbar" aria-label="正文格式工具栏">
      <template v-if="!advanced">
        <button type="button" title="加粗" :class="{ active: editor?.isActive('bold') }" @click="editor?.chain().focus().toggleBold().run()"><b>B</b></button>
        <button type="button" title="斜体" :class="{ active: editor?.isActive('italic') }" @click="editor?.chain().focus().toggleItalic().run()"><i>I</i></button>
        <button type="button" title="无序列表" @click="editor?.chain().focus().toggleBulletList().run()">• 列表</button>
        <button type="button" title="引用" @click="editor?.chain().focus().toggleBlockquote().run()">❝</button>
      </template>
      <button v-if="enableRedaction" type="button" data-testid="redaction-button" title="选中文字后打码；再次点击可取消" :class="{ active: !advanced && editor?.isActive('redaction') }" @click="toggleRedaction">▰ 打码</button>
      <button type="button" title="插入链接" @click="addLink">🔗</button>
      <span class="emoji-wrap"><button type="button" title="插入表情" @click="emojiOpen = !emojiOpen">😀</button><span v-if="emojiOpen" class="emoji-panel"><button v-for="emoji in emojis" :key="emoji" type="button" @click="insertEmoji(emoji)">{{ emoji }}</button></span></span>
      <label class="editor-upload" :class="{ disabled: busy || attachments.length >= maxImages }">{{ busy ? '上传中…' : '🖼️ 插图' }}<input type="file" accept="image/jpeg,image/png,image/webp" multiple :disabled="busy || attachments.length >= maxImages" @change="addImages" /></label>
      <template v-if="!advanced"><button type="button" title="撤销" @click="editor?.chain().focus().undo().run()">↶</button><button type="button" title="重做" @click="editor?.chain().focus().redo().run()">↷</button></template>
      <span class="editor-count">{{ modelValue.length }}/{{ maxLength }} · 图片 {{ attachments.length }}/{{ maxImages }}</span>
    </div>
    <EditorContent v-if="!advanced" :editor="editor" class="visual-editor" />
    <div v-else class="markdown-editor"><textarea ref="rawInput" v-model="markdown" :maxlength="maxLength" :placeholder="placeholder" rows="10" @input="rawChanged" @paste="pasteRaw" /><div class="markdown-preview"><b>{{ enableRedaction ? '公开打码预览' : '预览' }}</b><RichText :content="previewMarkdown" /></div></div>
    <p v-if="enableRedaction" class="redaction-help">选中姓名、联系方式或其他敏感文字后点击“打码”。公开页面显示为 ▓▓▓▓▓▓，符合权限的用户点击“去码查看”后才能看到原文。</p>
    <p v-if="error" class="notice danger">{{ error }}</p>
  </div>
</template>
