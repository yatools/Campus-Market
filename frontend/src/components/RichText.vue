<script setup lang="ts">
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { computed } from 'vue'

const props = defineProps<{ content: string }>()
marked.setOptions({ breaks: true, gfm: true })
const html = computed(() =>
  {
    const sanitized = DOMPurify.sanitize(marked.parse(props.content || '') as string, {
      ALLOWED_TAGS: ['p', 'br', 'strong', 'em', 's', 'blockquote', 'code', 'pre', 'ul', 'ol', 'li', 'a', 'img', 'h1', 'h2', 'h3', 'hr'],
      ALLOWED_ATTR: ['href', 'target', 'rel', 'src', 'alt', 'title', 'loading', 'referrerpolicy'],
    })
    const document = new DOMParser().parseFromString(sanitized, 'text/html')
    document.querySelectorAll('img').forEach((node) => {
      const src = node.getAttribute('src') || ''
      if (!src.startsWith('/uploads/')) { node.remove(); return }
      node.setAttribute('loading', 'lazy')
      node.setAttribute('referrerpolicy', 'no-referrer')
    })
    document.querySelectorAll('a').forEach((node) => {
      node.setAttribute('target', '_blank')
      node.setAttribute('rel', 'noopener noreferrer nofollow')
    })
    return document.body.innerHTML
  },
)
</script>

<template><div class="rich-text" v-html="html" /></template>
