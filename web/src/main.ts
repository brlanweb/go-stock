import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import Dashboard from './views/Dashboard.vue'
import StockDetail from './views/StockDetail.vue'
import Recommendations from './views/Recommendations.vue'
import SectorDetail from './views/SectorDetail.vue'
import './style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: Dashboard },
    { path: '/recommendations', component: Recommendations },
    { path: '/sector/:code', component: SectorDetail, props: true },
    { path: '/stock/:symbol', component: StockDetail, props: true }
  ]
})

createApp(App).use(router).mount('#app')
