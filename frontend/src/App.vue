<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import AuthModal from './components/AuthModal.vue'
import { useAuthStore } from './stores/auth'

const auth = useAuthStore()
onMounted(auth.load)
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <RouterLink to="/" class="brand"><span class="brand-mark">梧</span><span><strong>梧桐墙</strong><small>校园里的认真讨论</small></span></RouterLink>
      <nav class="desktop-nav" aria-label="主导航">
        <RouterLink to="/">树洞</RouterLink><RouterLink to="/teams">车队</RouterLink><RouterLink to="/explore">校园广场</RouterLink>
        <RouterLink v-if="auth.user" to="/messages">私信</RouterLink><RouterLink v-if="auth.user" to="/me">我的<span v-if="auth.user.unread_notifications" class="nav-count">{{ auth.user.unread_notifications }}</span></RouterLink>
        <RouterLink v-if="auth.canModerate" to="/admin">管理后台</RouterLink>
      </nav>
      <div class="account-actions">
        <template v-if="auth.user">
          <RouterLink to="/me" class="user-pill"><span>{{ auth.user.nickname.slice(0, 1) }}</span><b>{{ auth.user.nickname }}</b><small>信用 {{ auth.user.credit }}</small></RouterLink>
          <button class="text-button" @click="auth.logout">退出</button>
        </template>
        <button v-else class="button primary" @click="auth.authOpen = true">校邮登录</button>
      </div>
    </header>
    <main><RouterView /></main>
    <nav class="mobile-nav" aria-label="移动端导航">
      <RouterLink to="/">树洞</RouterLink><RouterLink to="/teams">车队</RouterLink><RouterLink to="/explore">广场</RouterLink><RouterLink to="/messages">私信</RouterLink><RouterLink to="/me">我的</RouterLink>
    </nav>
    <footer><span>梧桐墙 · 单校社区</span><span>线下面交，不经手资金 · 举报与申诉全程留痕</span></footer>
    <AuthModal v-if="auth.authOpen" />
  </div>
</template>
