import { createRouter, createWebHistory } from 'vue-router'
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'dashboard', component: () => import('./views/DashboardView.vue') },
    { path: '/treehole', name: 'treehole', component: () => import('./views/HomeView.vue') },
    { path: '/search', name: 'search', component: () => import('./views/SearchView.vue') },
    { path: '/teams/:id?', name: 'teams', component: () => import('./views/TeamsView.vue') },
    { path: '/explore/:section?', name: 'explore', component: () => import('./views/ExploreView.vue') },
    { path: '/messages/:id?', name: 'messages', component: () => import('./views/MessagesView.vue') },
    { path: '/me', name: 'me', component: () => import('./views/MeView.vue') },
    { path: '/admin', name: 'admin', component: () => import('./views/AdminView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: () => ({ top: 0 }),
})
