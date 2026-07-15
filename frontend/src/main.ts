import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import './styles.css'
import { loadAppConfig } from './config'

async function bootstrap() {
  await loadAppConfig()
  createApp(App).use(createPinia()).use(router).mount('#app')
}

bootstrap().catch((error) => {
  const root = document.querySelector('#app')
  if (root) root.textContent = error instanceof Error ? error.message : '应用启动失败'
})
