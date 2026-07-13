import { createRouter, createWebHistory } from 'vue-router'
import HomeView from './views/HomeView.vue'
import TeamsView from './views/TeamsView.vue'
import ExploreView from './views/ExploreView.vue'
import MessagesView from './views/MessagesView.vue'
import MeView from './views/MeView.vue'
import AdminView from './views/AdminView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    { path: '/teams/:id?', name: 'teams', component: TeamsView },
    { path: '/explore/:section?', name: 'explore', component: ExploreView },
    { path: '/messages/:id?', name: 'messages', component: MessagesView },
    { path: '/me', name: 'me', component: MeView },
    { path: '/admin', name: 'admin', component: AdminView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

