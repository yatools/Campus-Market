<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api'
import type { Page, SearchResult } from '../types'

const route = useRoute()
const rows = ref<SearchResult[]>([])
const loading = ref(false)
const error = ref('')
const total = ref(0)
const query = computed(() => String(route.query.q || '').trim())

const typeInfo: Record<string, { icon: string; name: string; path: string }> = {
  post: { icon: '🌳', name: '树洞', path: '/treehole' }, question: { icon: '🙋', name: '问答', path: '/explore/questions' },
  handbook: { icon: '📖', name: '生存手册', path: '/explore/handbook' }, listing: { icon: '🛒', name: '二手集市', path: '/explore/listings' },
  activity: { icon: '🎪', name: '校园活动', path: '/explore/activities' }, lost: { icon: '🧣', name: '失物招领', path: '/explore/lost' },
}

function info(type: string) { return typeInfo[type] || { icon: '📌', name: type, path: '/' } }

async function load() {
  if (!query.value) { rows.value = []; total.value = 0; return }
  loading.value = true; error.value = ''
  try {
    const result = await api<Page<SearchResult>>(`/search?q=${encodeURIComponent(query.value)}&page_size=50`)
    rows.value = result.items; total.value = result.total
  } catch (e) { error.value = e instanceof Error ? e.message : '搜索失败' }
  finally { loading.value = false }
}

watch(query, load, { immediate: true })
</script>

<template>
  <section>
    <header class="page-head"><h1>🔎 全站搜索</h1><p v-if="query">“{{ query }}”找到 {{ total }} 条公开内容；匿名帖子不会出现在结果中。</p><p v-else>在顶部输入关键词搜索公开内容。</p></header>
    <p v-if="error" class="notice danger">{{ error }}</p><p v-if="loading" class="empty-state">正在翻找校园公告栏…</p>
    <div v-else class="stack search-results">
      <article v-for="item in rows" :key="item.id" class="card search-result">
        <span class="search-icon">{{ info(item.type).icon }}</span><div><span class="badge">{{ info(item.type).name }}</span><h2>{{ item.title }}</h2><p>{{ item.summary }}</p><RouterLink :to="info(item.type).path">前往{{ info(item.type).name }} →</RouterLink></div>
      </article>
      <p v-if="query && !rows.length" class="empty-state">没有找到相关公开内容，换个关键词试试。</p>
    </div>
  </section>
</template>
