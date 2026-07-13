<script setup lang="ts">
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { computed } from 'vue'

const props = defineProps<{ content: string }>()
marked.setOptions({ breaks: true, gfm: true })
const html = computed(() =>
  DOMPurify.sanitize(marked.parse(props.content || '') as string, {
    ALLOWED_TAGS: ['p', 'br', 'strong', 'em', 'blockquote', 'code', 'pre', 'ul', 'ol', 'li', 'a'],
    ALLOWED_ATTR: ['href', 'target', 'rel'],
  }),
)
</script>

<template><div class="rich-text" v-html="html" /></template>

