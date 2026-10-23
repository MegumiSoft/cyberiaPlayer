import { pal, palWrap, rgba } from './palette'

export type VisMode = 'bars' | 'scope' | 'mandala' | 'spiro' | 'tunnel'

export const VIS_MODES: { id: VisMode; label: string }[] = [
  { id: 'bars', label: 'BARS' },
  { id: 'scope', label: 'SCOPE' },
  { id: 'mandala', label: 'MANDALA' },
  { id: 'spiro', label: 'SPIRO' },
  { id: 'tunnel', label: 'TUNNEL' },
]

/** Datos de audio de un instante. Todos los valores de energía están en [0,1]. */
export interface Frame {
  freq: Uint8Array // espectro (0-255 por banda)
  wave: Uint8Array // forma de onda (128 = silencio)
  level: number // energía global
  bass: number
  mid: number
  treble: number
  beat: number // 1 en un golpe de bajo y decae a 0
  time: number // segundos
  dt: number // segundos desde el cuadro anterior
  active: boolean // false si no está sonando nada
}

export function emptyFrame(): Frame {
  return {
    freq: new Uint8Array(1024), wave: new Uint8Array(2048).fill(128),
    level: 0, bass: 0, mid: 0, treble: 0, beat: 0, time: 0, dt: 1 / 60, active: false,
  }
}

const BG = '11,11,15'
const TAU = Math.PI * 2

/** Energía de la banda alrededor de un índice del espectro, suavizada con sus vecinas. */
function band(f: Frame, i: number): number {
  const n = f.freq.length
  const a = f.freq[Math.max(0, Math.min(n - 1, i - 1))]
  const b = f.freq[Math.max(0, Math.min(n - 1, i))]
  const c = f.freq[Math.max(0, Math.min(n - 1, i + 1))]
  return (a + b * 2 + c) / 1020
}

export class Visualizer {
  private ctx: CanvasRenderingContext2D
  private w = 1
  private h = 1
  private dpr = 1
  private bars = new Float32Array(0)
  private peaks = new Float32Array(0)
  private rot = 0
  private hue = 0
  private phase = 0
  private tz = 0
  private sm = { bass: 0, mid: 0, treble: 0, level: 0 }
  private lastMode: VisMode | null = null

  constructor(private canvas: HTMLCanvasElement) {
    const c = canvas.getContext('2d')
    if (!c) throw new Error('El canvas 2D no está disponible')
    this.ctx = c
  }

  resize(cssW: number, cssH: number, dpr: number): void {
    this.dpr = dpr
    this.w = Math.max(1, Math.floor(cssW * dpr))
    this.h = Math.max(1, Math.floor(cssH * dpr))
    this.canvas.width = this.w
    this.canvas.height = this.h
    this.clear()
  }

  private clear(): void {
    this.ctx.fillStyle = `rgb(${BG})`
    this.ctx.fillRect(0, 0, this.w, this.h)
  }

  draw(mode: VisMode, f: Frame): void {
    const k = 1 - Math.exp(-f.dt * 9)
    this.sm.bass += (f.bass - this.sm.bass) * k
    this.sm.mid += (f.mid - this.sm.mid) * k
    this.sm.treble += (f.treble - this.sm.treble) * k
    this.sm.level += (f.level - this.sm.level) * k
    this.hue += f.dt * 0.035
    if (mode !== this.lastMode) {
      this.clear()
      this.lastMode = mode
    }
    this.ctx.save()
    switch (mode) {
      case 'bars':
        this.drawBars(f)
        break
      case 'scope':
        this.drawScope(f)
        break
      case 'mandala':
        this.drawMandala(f)
        break
      case 'spiro':
        this.drawSpiro(f)
        break
      case 'tunnel':
        this.drawTunnel(f)
        break
    }
    this.ctx.restore()
  }

  // ---------------------------------------------------------------- BARS
  // Barras segmentadas como en el analizador clásico: bloques con degradado vertical y picos que caen.
  private drawBars(f: Frame): void {
    const { ctx, w, h, dpr } = this
    this.clear()
    const n = Math.max(28, Math.min(80, Math.floor(w / (12 * dpr))))
    if (this.bars.length !== n) {
      this.bars = new Float32Array(n)
      this.peaks = new Float32Array(n)
    }
    const gap = 2 * dpr
    const bw = (w - gap * (n - 1)) / n
    const blockH = 5 * dpr
    const blockGap = 2 * dpr
    const rows = Math.max(4, Math.floor((h - 8 * dpr) / (blockH + blockGap)))
    const len = f.freq.length
    const lo = 1
    const hi = Math.floor(len * 0.8)

    for (let i = 0; i < n; i++) {
      const a = Math.floor(lo * Math.pow(hi / lo, i / n))
      const b = Math.max(a + 1, Math.floor(lo * Math.pow(hi / lo, (i + 1) / n)))
      let m = 0
      for (let j = a; j < b; j++) m = Math.max(m, f.freq[j])
      let v = Math.min(1, (m / 255) * (0.8 + 0.6 * (i / n)))
      if (!f.active) v = 0.03 + 0.02 * Math.sin(f.time * 1.6 + i * 0.45) // respiración en reposo
      if (v >= this.bars[i]) this.bars[i] += (v - this.bars[i]) * 0.7
      else this.bars[i] = Math.max(v, this.bars[i] - f.dt * 1.3)
      this.peaks[i] = Math.max(this.bars[i], this.peaks[i] - f.dt * 0.4)

      const x = i * (bw + gap)
      const lit = Math.round(this.bars[i] * rows)
      for (let r = 0; r < lit; r++) {
        ctx.fillStyle = rgba(pal(r / (rows - 1)), 0.96)
        ctx.fillRect(x, h - (r + 1) * (blockH + blockGap), bw, blockH)
      }
      const pr = Math.round(this.peaks[i] * rows) - 1
      if (pr >= lit && pr > 0) {
        ctx.fillStyle = 'rgba(255,255,255,0.8)'
        ctx.fillRect(x, h - (pr + 1) * (blockH + blockGap), bw, blockH)
      }
    }
  }

  // ---------------------------------------------------------------- SCOPE
  // Osciloscopio con persistencia de fósforo y brillo.
  private drawScope(f: Frame): void {
    const { ctx, w, h, dpr } = this
    ctx.fillStyle = `rgba(${BG},0.3)`
    ctx.fillRect(0, 0, w, h)

    ctx.strokeStyle = 'rgba(255,255,255,0.045)'
    ctx.lineWidth = dpr
    ctx.beginPath()
    for (let i = 1; i < 8; i++) {
      ctx.moveTo((w * i) / 8, 0)
      ctx.lineTo((w * i) / 8, h)
    }
    for (let j = 1; j < 4; j++) {
      ctx.moveTo(0, (h * j) / 4)
      ctx.lineTo(w, (h * j) / 4)
    }
    ctx.stroke()

    const g = ctx.createLinearGradient(0, 0, w, 0)
    for (let s = 0; s <= 6; s++) g.addColorStop(s / 6, rgba(palWrap(s / 6 + this.hue * 0.6), 1))

    const n = f.wave.length
    const amp = h * 0.4 * (f.active ? 1.5 : 1)
    const path = new Path2D()
    for (let x = 0; x <= w; x += 2 * dpr) {
      const idx = Math.min(n - 1, Math.floor((x / w) * n))
      let v = (f.wave[idx] - 128) / 128
      if (!f.active) v += Math.sin((x / w) * 12 + f.time * 2.2) * 0.07
      const y = h / 2 + v * amp
      if (x === 0) path.moveTo(x, y)
      else path.lineTo(x, y)
    }
    ctx.lineJoin = 'round'
    ctx.lineWidth = 2.4 * dpr
    ctx.strokeStyle = g
    ctx.shadowBlur = 16 * dpr * (0.35 + this.sm.level)
    ctx.shadowColor = rgba(palWrap(this.hue * 0.6), 0.9)
    ctx.stroke(path)

    ctx.shadowBlur = 0
    ctx.globalAlpha = 0.28
    ctx.lineWidth = dpr
    ctx.translate(0, h)
    ctx.scale(1, -1)
    ctx.stroke(path)
  }

  // ---------------------------------------------------------------- MANDALA
  // Simetría radial de 10 pétalos espejados: tres anillos que leen distintas zonas del espectro.
  private drawMandala(f: Frame): void {
    const { ctx, w, h, dpr } = this
    ctx.fillStyle = `rgba(${BG},${0.1 + (1 - this.sm.level) * 0.06})`
    ctx.fillRect(0, 0, w, h)
    this.rot += f.dt * (0.1 + this.sm.treble * 1.4)
    const R = Math.min(w, h) * 0.47
    const K = 10
    const M = 44
    const layers = [
      { r0: 0.16, amp: 0.3, b0: 2, b1: 56, hue: 0 },
      { r0: 0.4, amp: 0.34, b0: 18, b1: 200, hue: 0.28 },
      { r0: 0.66, amp: 0.3, b0: 90, b1: 520, hue: 0.56 },
    ]
    ctx.translate(w / 2, h / 2)
    ctx.rotate(this.rot)
    ctx.globalCompositeOperation = 'lighter'
    ctx.lineWidth = 1.5 * dpr
    ctx.lineJoin = 'round'

    layers.forEach((L, li) => {
      const pts: [number, number][] = []
      for (let i = 0; i <= M; i++) {
        const t = i / M
        const ang = (t * Math.PI) / K
        const idx = Math.floor(L.b0 + (L.b1 - L.b0) * t)
        const raw = f.active ? Math.pow(band(f, idx), 0.85) : 0.06 + 0.05 * Math.sin(f.time * 1.1 + t * 7 + li)
        const wob = Math.sin(ang * K * 3 + f.time * (1.2 + li * 0.4)) * 0.018
        const r = R * (L.r0 + L.amp * raw + wob) * (1 + f.beat * 0.07)
        pts.push([Math.cos(ang) * r, Math.sin(ang) * r])
      }
      for (let k = 0; k < K; k++) {
        ctx.strokeStyle = rgba(palWrap(this.hue + L.hue + (k / K) * 0.5), 0.22 + 0.3 * this.sm.level)
        for (const mirror of [1, -1]) {
          ctx.save()
          ctx.rotate((k * TAU) / K)
          ctx.scale(1, mirror)
          ctx.beginPath()
          pts.forEach(([x, y], i) => (i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y)))
          ctx.stroke()
          ctx.restore()
        }
      }
    })

    if (f.beat > 0.02) {
      ctx.strokeStyle = rgba(palWrap(this.hue + 0.5), f.beat * 0.55)
      ctx.lineWidth = (1 + f.beat * 3) * dpr
      ctx.beginPath()
      ctx.arc(0, 0, R * (0.12 + (1 - f.beat) * 0.88), 0, TAU)
      ctx.stroke()
    }
    const gr = ctx.createRadialGradient(0, 0, 0, 0, 0, R * 0.2 * (1 + this.sm.bass))
    gr.addColorStop(0, rgba(palWrap(this.hue + 0.2), 0.3))
    gr.addColorStop(1, rgba(palWrap(this.hue + 0.2), 0))
    ctx.fillStyle = gr
    ctx.beginPath()
    ctx.arc(0, 0, R * 0.2 * (1 + this.sm.bass), 0, TAU)
    ctx.fill()
  }

  // ---------------------------------------------------------------- SPIRO
  // Hipotrocoides (espirógrafo) cuyos parámetros derivan lentamente y reaccionan a graves, medios y agudos.
  private drawSpiro(f: Frame): void {
    const { ctx, w, h, dpr } = this
    ctx.fillStyle = `rgba(${BG},${0.07 + (1 - this.sm.level) * 0.04})`
    ctx.fillRect(0, 0, w, h)
    this.phase += f.dt * (0.35 + this.sm.mid * 1.6)
    const t = this.phase
    const R = Math.min(w, h) * 0.44
    ctx.translate(w / 2, h / 2)
    ctx.rotate(t * 0.12)
    ctx.globalCompositeOperation = 'lighter'
    ctx.lineWidth = 1.3 * dpr
    ctx.lineJoin = 'round'

    const curves = [
      { b: 0.31 + 0.17 * Math.sin(t * 0.07), d: 0.55 + 0.35 * this.sm.bass + 0.1 * Math.sin(t * 0.5), s: 1, h: 0 },
      { b: 0.43 + 0.12 * Math.sin(t * 0.05 + 2), d: 0.4 + 0.3 * this.sm.mid + 0.08 * Math.sin(t * 0.4 + 1), s: 0.62, h: 0.45 },
    ]
    const N = 1500
    const chunk = 50
    const loops = 7
    for (const c of curves) {
      const a = 1
      const diff = a - c.b
      const scale = ((R * c.s) / (diff + c.d)) * (1 + f.beat * 0.07)
      let px = 0
      let py = 0
      for (let i = 0; i <= N; i++) {
        const th = (i / N) * TAU * loops
        const x = (diff * Math.cos(th) + c.d * Math.cos((diff / c.b) * th)) * scale
        const y = (diff * Math.sin(th) - c.d * Math.sin((diff / c.b) * th)) * scale
        if (i % chunk === 0) {
          if (i > 0) ctx.stroke()
          ctx.strokeStyle = rgba(palWrap(this.hue + c.h + (i / N) * 0.8), 0.12 + 0.3 * this.sm.treble)
          ctx.beginPath()
          ctx.moveTo(px, py)
        }
        ctx.lineTo(x, y)
        px = x
        py = y
      }
      ctx.stroke()
    }
  }

  // ---------------------------------------------------------------- TUNNEL
  // Polígonos que se acercan: el espectro deforma cada vértice y los graves aceleran el viaje.
  private drawTunnel(f: Frame): void {
    const { ctx, w, h, dpr } = this
    ctx.fillStyle = `rgba(${BG},0.26)`
    ctx.fillRect(0, 0, w, h)
    this.tz = (this.tz + f.dt * (0.1 + this.sm.level * 0.5 + f.beat * 0.5)) % 1
    this.phase += f.dt
    const maxR = Math.hypot(w, h) * 0.55
    const rings = 22
    const sides = 6
    ctx.translate(w / 2, h / 2)
    ctx.globalCompositeOperation = 'lighter'
    ctx.lineJoin = 'round'

    for (let i = 0; i < rings; i++) {
      const z = (i / rings + this.tz) % 1
      const radius = maxR * z * z * (1 + this.sm.bass * 0.25)
      const rot = z * 1.7 + this.phase * 0.15 + Math.sin(this.phase * 0.2)
      ctx.beginPath()
      for (let j = 0; j <= sides; j++) {
        const ang = rot + (j % sides) * (TAU / sides)
        const idx = 2 + Math.floor(((j % sides) / sides) * 220)
        const v = f.active ? band(f, idx) : 0.05
        const r = radius * (1 + 0.4 * v * z)
        const x = Math.cos(ang) * r
        const y = Math.sin(ang) * r
        if (j === 0) ctx.moveTo(x, y)
        else ctx.lineTo(x, y)
      }
      ctx.lineWidth = (0.5 + z * 2.8) * dpr
      ctx.strokeStyle = rgba(palWrap(z * 0.9 + this.hue * 1.5), Math.min(1, z * 1.1))
      ctx.stroke()
    }
  }
}
