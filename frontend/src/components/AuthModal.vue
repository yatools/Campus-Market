<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api, json } from '../api'
import { useAuthStore } from '../stores/auth'
import type { AuthMode, RegisterRequest, User, VerificationCodeRequest } from '../types'
import BaseModal from './BaseModal.vue'

const auth = useAuthStore()
const props = withDefaults(defineProps<{ initialMode?: AuthMode }>(), { initialMode: 'login' })
const mode = ref<AuthMode>(props.initialMode)
const busy = ref(false)
const message = ref('')
const error = ref('')
const form = reactive({ email: '', password: '', nickname: '', code: '', newPassword: '' })
const title = computed(() => mode.value === 'login' ? '登录' : mode.value === 'register' ? '注册梧桐墙' : '重置密码')
const subtitle = computed(() => mode.value === 'login' ? '欢迎回来' : mode.value === 'register' ? '使用校园邮箱验证身份，注册后自动登录' : '验证码通过后会使所有旧设备退出登录')

watch(() => props.initialMode, (value) => { mode.value = value })

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
  <BaseModal :title="title" @close="auth.authOpen = false">
    <p class="modal-sub auth-sub-v4">{{ subtitle }}</p>
    <form class="form-stack" @submit.prevent="submit">
      <div class="auth-email-row-v4"><label>校园邮箱<input v-model.trim="form.email" type="email" required autocomplete="email" placeholder="you@stu.xxxx.edu.cn" /></label><button v-if="mode !== 'login'" type="button" class="btn ghost" @click="sendCode(mode === 'register' ? 'register' : 'reset_password')">发送验证码</button></div>
      <template v-if="mode !== 'login'">
        <label>邮箱验证码<input v-model.trim="form.code" required pattern="\d{6}" inputmode="numeric" placeholder="6 位数字，10 分钟内有效" /></label>
        <label v-if="mode === 'register'">昵称<input v-model.trim="form.nickname" required minlength="2" maxlength="20" placeholder="你的公开昵称" /></label>
      </template>
      <label v-if="mode !== 'reset'">密码<input v-model="form.password" type="password" required :minlength="mode === 'register' ? 10 : 1" autocomplete="current-password" /></label>
      <label v-else>新密码<input v-model="form.newPassword" type="password" required minlength="10" autocomplete="new-password" /></label>
      <label v-if="mode === 'register'" class="check auth-agreement-v4"><input type="checkbox" required /> 我已阅读并同意《用户协议》与《社区规范》，理解后台将保留账号、发帖、处罚和举报记录用于治理，前台不公开真实身份。</label>
      <p v-if="message" class="notice success">{{ message }}</p>
      <p v-if="error" class="notice danger">{{ error }}</p>
      <button class="btn primary auth-submit-v4" :disabled="busy">{{ busy ? '处理中…' : mode === 'login' ? '登录' : mode === 'register' ? '注册并自动登录' : '重置密码' }}</button>
    </form>
    <p class="auth-switch-v4"><template v-if="mode === 'login'">没有账号？<button @click="mode = 'register'">邮箱验证码注册 →</button> <button @click="mode = 'reset'">忘记密码</button></template><template v-else>已有账号？<button @click="mode = 'login'">直接登录 →</button></template></p>
  </BaseModal>
</template>
