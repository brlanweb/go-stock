import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import Dashboard from './views/Dashboard.vue'
import StockDetail from './views/StockDetail.vue'
import SectorDetail from './views/SectorDetail.vue'
import './style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: Dashboard },
    // 三个视图已并入首页 Tab，旧路径重定向保持外部链接与书签有效
    { path: '/recommendations', redirect: { path: '/', query: { view: 'reco' } } },
    { path: '/review', redirect: { path: '/', query: { view: 'review' } } },
    { path: '/indicators', redirect: { path: '/', query: { view: 'indicators' } } },
    { path: '/sector/:code', component: SectorDetail, props: true },
    { path: '/stock/:symbol', component: StockDetail, props: true }
  ]
})

createApp(App).use(router).mount('#app')
