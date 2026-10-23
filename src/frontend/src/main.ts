import { createApp } from 'vue'
import App from './App.vue'
import './style.css'

// Escala de las letras según el tamaño de la ventana (más grande al maximizar); Ctrl/Cmd +/−/0 la ajustan a mano.
let userScale = 1
try {
  userScale = Number(localStorage.getItem('cyberia.fs')) || 1
} catch {
  /* sin almacenamiento */
}
function applyScale() {
  const auto = Math.min(1.6, Math.max(1.15, (1.15 * window.innerWidth) / 1100))
  document.documentElement.style.setProperty('--fs', (auto * userScale).toFixed(3))
}
window.addEventListener('resize', applyScale)
window.addEventListener('keydown', (e) => {
  if (!(e.ctrlKey || e.metaKey) || e.altKey) return
  const k = e.key
  if (k === '+' || k === '=' || k === '-' || k === '_' || k === '0') {
    e.preventDefault()
    userScale = k === '0' ? 1 : Math.min(1.6, Math.max(0.8, userScale + (k === '-' || k === '_' ? -0.1 : 0.1)))
    try {
      localStorage.setItem('cyberia.fs', String(userScale))
    } catch {
      /* sin almacenamiento */
    }
    applyScale()
  }
})
applyScale()

createApp(App).mount('#app')
