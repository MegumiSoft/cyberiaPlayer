<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CHARACTERS, fxUrl, spriteUrl, type CharDef, type CharId } from '../player/characters'
import { beatState } from '../player/engine'

const props = defineProps<{ character: CharId; playing: boolean; active: boolean }>()

const canvas = ref<HTMLCanvasElement>()
const SIZE = 160

interface Sheet {
  def: CharDef
  idle: HTMLImageElement[]
  dances: HTMLImageElement[][]
  jump: HTMLImageElement[]
  fx: HTMLImageElement[]
}

let sheet: Sheet | null = null
let loadId = 0

const img = (src: string): HTMLImageElement => {
  const i = new Image()
  i.src = src
  return i
}
const seq = (char: string, dir: string, n: number) => Array.from({ length: n }, (_, k) => img(spriteUrl(char, dir, k + 1)))

function load(id: CharId) {
  const def = CHARACTERS[id]
  const mine = ++loadId
  const s: Sheet = {
    def,
    idle: seq(id, 'idle', def.idle),
    dances: def.dances.map((d) => seq(id, d.dir, d.frames)),
    jump: def.jump ? seq(id, def.jump.dir, def.jump.frames) : [],
    fx: def.fx.map((f) => img(fxUrl(id, f))),
  }
  if (mine === loadId) sheet = s
}

interface Puff {
  img: HTMLImageElement
  x: number
  y: number
  vx: number
  vy: number
  life: number
}
const puffs: Puff[] = []

let raf = 0
let last = 0
let pos = 0 // posición en pulsos
let lastCount = 0
let jumpUntil = 0
let wasPlaying = false

function ready(i: HTMLImageElement | undefined): i is HTMLImageElement {
  return !!i && i.complete && i.naturalWidth > 0
}

function loop(ts: number) {
  raf = requestAnimationFrame(loop)
  const c = canvas.value
  const s = sheet
  if (!c || !s) return
  const now = ts / 1000
  const dt = Math.min(0.1, last ? now - last : 1 / 60)
  last = now
  const ctx = c.getContext('2d')!
  ctx.imageSmoothingEnabled = false
  ctx.clearRect(0, 0, SIZE, SIZE)

  const playing = props.playing
  let frame: HTMLImageElement | undefined
  let bounce = 0
  let tilt = 0
  let squash = 1
  let flip = 1
  let sway = 0
  let glow = 0.18

  if (playing) {
    pos += (dt * beatState.bpm) / 60
    if (beatState.count !== lastCount) {
      lastCount = beatState.count
      pos += (Math.round(pos) - pos) * 0.6 // se engancha al golpe real del audio
      if (s.fx.length && Math.random() < 0.7) {
        const f = s.fx[Math.floor(Math.random() * s.fx.length)]
        puffs.push({ img: f, x: 68 + Math.random() * 44, y: 44, vx: (Math.random() - 0.5) * 18, vy: -(26 + Math.random() * 20), life: 1 })
      }
    }
    if (!wasPlaying) pos = 0
    const beatIdx = Math.floor(pos)
    const frac = pos - beatIdx
    // dos coreografías que se alternan cada 8 pulsos
    const di = Math.floor(pos / 8) % s.dances.length
    const dance = s.dances[di]
    const style = s.def.dances[di].style
    const hop = beatIdx % 16 === 15 // cada 16 pulsos, un salto
    if (hop && s.jump.length && now > jumpUntil) jumpUntil = now + 60 / beatState.bpm
    if (s.jump.length && now < jumpUntil) {
      const j = Math.min(s.jump.length - 1, Math.floor(((jumpUntil - now) / (60 / beatState.bpm)) * s.jump.length))
      frame = s.jump[s.jump.length - 1 - j]
      bounce = Math.sin(Math.PI * frac) * 12
    } else {
      const fi = Math.floor(((pos % 4) / 4) * dance.length) % dance.length
      frame = dance[s.def.dances[di].reverse ? dance.length - 1 - fi : fi]
      if (style === 'bounce') {
        bounce = Math.abs(Math.sin(Math.PI * frac)) * 7
        tilt = (beatIdx % 2 ? 1 : -1) * 0.07 * (1 - frac)
        squash = 1 - 0.06 * Math.max(0, 1 - frac * 5)
        if (s.def.flip && Math.floor(pos / 2) % 2) flip = -1
      } else {
        // balanceo lateral: se inclina y se desplaza de lado a lado cada dos pulsos
        const sw = Math.sin(pos * Math.PI)
        sway = sw * 12
        tilt = sw * 0.16
        bounce = Math.abs(Math.sin(Math.PI * frac)) * 3.5
        squash = 1 - 0.035 * Math.abs(sw)
        if (s.def.flip) flip = sw >= 0 ? 1 : -1
      }
      if (hop && !s.jump.length) {
        // sin cuadros de salto: giro completo con brinco alto
        bounce = Math.sin(Math.PI * frac) * 20
        flip = Math.cos(frac * Math.PI * 2)
      }
    }
    glow = 0.22 + 0.25 * (1 - frac)
  } else {
    // En pausa: respira en el sitio con la animación idle.
    frame = s.idle[Math.floor(now * 6) % s.idle.length]
    bounce = (Math.sin(now * 2.2) + 1) * 0.9
    pos = 0
  }
  wasPlaying = playing

  // sombra / resplandor de piso
  ctx.save()
  ctx.translate(SIZE / 2 + sway, SIZE - 5)
  ctx.scale(1, 0.22)
  const g = ctx.createRadialGradient(0, 0, 0, 0, 0, 40 - bounce)
  g.addColorStop(0, `rgba(24,224,255,${glow})`)
  g.addColorStop(1, 'rgba(24,224,255,0)')
  ctx.fillStyle = g
  ctx.beginPath()
  ctx.arc(0, 0, 40 - bounce, 0, Math.PI * 2)
  ctx.fill()
  ctx.restore()

  if (ready(frame)) {
    ctx.save()
    ctx.translate(SIZE / 2 + sway, SIZE - 4 - bounce) // pivote en los pies
    ctx.rotate(tilt)
    ctx.scale(flip, squash)
    const d = s.def.drawSize
    if (s.def.framed) {
      // cuadros con fondo: tarjeta redondeada con borde de neón que late con el pulso
      const x = -d / 2
      const y = -d + 4
      ctx.beginPath()
      ctx.roundRect(x, y, d, d, 10)
      ctx.save()
      ctx.clip()
      ctx.drawImage(frame, x, y, d, d)
      ctx.restore()
      ctx.lineWidth = 2
      ctx.strokeStyle = `rgba(24,224,255,${0.55 + 0.4 * glow})`
      ctx.shadowColor = 'rgba(255,47,176,0.8)'
      ctx.shadowBlur = 6 + 8 * glow
      ctx.stroke()
    } else {
      ctx.drawImage(frame, -d / 2, -d + 4, d, d)
    }
    ctx.restore()
  }

  for (let i = puffs.length - 1; i >= 0; i--) {
    const p = puffs[i]
    p.life -= dt * 1.1
    p.x += p.vx * dt
    p.y += p.vy * dt
    if (p.life <= 0) {
      puffs.splice(i, 1)
      continue
    }
    if (ready(p.img)) {
      ctx.globalAlpha = Math.min(1, p.life * 1.5)
      ctx.drawImage(p.img, p.x, p.y, 20, 20)
      ctx.globalAlpha = 1
    }
  }
}

function run() {
  if (raf || !props.active) return
  last = 0
  raf = requestAnimationFrame(loop)
}
function halt() {
  cancelAnimationFrame(raf)
  raf = 0
}

onMounted(() => {
  load(props.character)
  run()
})
onBeforeUnmount(halt)
watch(
  () => props.character,
  (c) => load(c),
)
watch(
  () => props.active,
  (a) => (a ? run() : halt()),
)
</script>

<template>
  <canvas ref="canvas" class="dancer" :width="SIZE" :height="SIZE" aria-hidden="true" />
</template>
