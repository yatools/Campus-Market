<script setup lang="ts">
import { computed } from 'vue'
import type { Attachment } from '../types'

const props = defineProps<{ content?: string; attachments?: Attachment[] }>()
const visible = computed(() => (props.attachments || []).filter((item) => !(props.content || '').includes(item.url)))
</script>

<template>
  <div v-if="visible.length" class="media-grid"><a v-for="item in visible" :key="item.id" :href="item.url" target="_blank" rel="noopener"><img :src="item.thumbnail_url" alt="内容附件" loading="lazy" /></a></div>
</template>
