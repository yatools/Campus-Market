<script setup lang="ts">
import { reactive, ref } from 'vue'
import { api, json } from '../api'
import { useAuthStore } from '../stores/auth'
import type { RegisterRequest, User, VerificationCodeRequest } from '../types'
import BaseModal from './BaseModal.vue'

const auth = useAuthStore()
const mode = ref<'login' | 'register' | 'reset'>('login')
const busy = ref(false)
const message = ref('')
const error = ref('')
const form = reactive({ email: '', password: '', nickname: '', code: '', newPassword: '' })

async function sendCode(purpose: 'register' | 'reset_password') {
  error.value = ''
  const payload: VerificationCodeRequest = { email: form.email, purpose }
  await api('/auth/request-code', json('POST', payload))
  message.value = '验证码已发送，请检查校园邮箱（10 分钟内有效）。'
}

async function submit() {
  busy.value = true
  error.value = ''
  try {
    if (mode.value === 'login') {
      await auth.login(form.email, form.password)
    } else if (mode.value === 'register') {
      const payload: RegisterRequest = {
        email: form.email,
        code: form.code,
        password: form.password,
        nickname: form.nickname,
        agreed_terms_version: '2026-07',
      }
      const result = await api<{ user: User }>('/auth/register', json('POST', payload))
      await auth.load()
      auth.authOpen = false
      void result
    } else {
      await api('/auth/reset-password', json('POST', { email: form.email, code: form.code, new_password: form.newPassword }))
      message.value = '密码已重置，请重新登录。'
      mode.value = 'login'
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : '操作失败'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <BaseModal title="校园身份登录" @close="auth.authOpen = false">
    <div class="segmented">
      <button :class="{ active: mode === 'login' }" @click="mode = 'login'">登录</button>
      <button :class="{ active: mode === 'register' }" @click="mode = 'register'">注册</button>
      <button :class="{ active: mode === 'reset' }" @click="mode = 'reset'">忘记密码</button>
    </div>
    <form class="form-stack" @submit.prevent="submit">
      <label>校园邮箱<input v-model.trim="form.email" type="email" required autocomplete="email" /></label>
      <template v-if="mode !== 'login'">
        <label v-if="mode === 'register'">昵称<input v-model.trim="form.nickname" required minlength="2" maxlength="20" /></label>
        <div class="inline-field">
          <label>验证码<input v-model.trim="form.code" required pattern="\d{6}" inputmode="numeric" /></label>
          <button type="button" class="button secondary" @click="sendCode(mode === 'register' ? 'register' : 'reset_password')">发送验证码</button>
        </div>
      </template>
      <label v-if="mode !== 'reset'">密码<input v-model="form.password" type="password" required :minlength="mode === 'register' ? 10 : 1" autocomplete="current-password" /></label>
      <label v-else>新密码<input v-model="form.newPassword" type="password" required minlength="10" autocomplete="new-password" /></label>
      <label v-if="mode === 'register'" class="check"><input type="checkbox" required /> 我已阅读并同意用户协议、隐私政策和社区规范</label>
      <p v-if="message" class="notice success">{{ message }}</p>
      <p v-if="error" class="notice danger">{{ error }}</p>
      <button class="button primary" :disabled="busy">{{ busy ? '处理中…' : mode === 'login' ? '登录' : mode === 'register' ? '验证并注册' : '重置密码' }}</button>
    </form>
  </BaseModal>
</template>
