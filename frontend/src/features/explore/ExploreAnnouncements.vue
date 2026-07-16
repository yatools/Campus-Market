<script setup lang="ts">
import RichText from '../../components/RichText.vue'
import type { Announcement } from '../../types'

defineProps<{ items: Announcement[]; formatDate: (value: string) => string }>()
defineEmits<{ read: [item: Announcement] }>()
</script>

<template>
  <p v-if="!items.length" class="empty-state">暂无公告。</p>
  <article v-for="item in items" :key="item.id" class="card announcement-card-v4" :class="{ strong: item.level === 'strong' }">
    <h3>{{ item.title }} <span class="tag" :class="item.level === 'strong' ? 'red' : 'gray'">{{ item.level === 'strong' ? '强提醒' : '普通公告' }}</span></h3>
    <RichText :content="item.body" />
    <p class="muted">{{ formatDate(item.published_at) }} · 已读确认：<b class="mono">{{ item.read_count }}</b> 人 <button v-if="!item.read" class="btn ghost sm" @click="$emit('read', item)">我已阅读</button><span v-else class="tag green">✓ 已确认</span></p>
  </article>
</template>
