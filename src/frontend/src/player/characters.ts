// Mascotas del visualizador. Los cuadros están en public/sprites/<personaje>/<animación>/NN.png.
export type CharId = 'miku' | 'gir' | 'cat'

export interface CharDef {
  id: CharId
  name: string
  idle: number
  /** Dos coreografías que se alternan cada 8 pulsos (cada una se reparte en 4 pulsos). */
  dances: { dir: string; frames: number; style: 'bounce' | 'sway'; reverse?: boolean }[]
  jump: { dir: string; frames: number } | null
  fx: string[]
  /** Gira el cuadro en horizontal cada 2 pulsos para dar más vida (solo si el dibujo lo tolera). */
  flip: boolean
  /** Lado (px) con el que se dibuja el cuadro dentro del lienzo de 128 px. */
  drawSize: number
  /** Cuadros con fondo (no son recortes): se dibujan dentro de una tarjeta con borde de neón. */
  framed?: boolean
}

export const CHARACTERS: Record<CharId, CharDef> = {
  miku: {
    id: 'miku',
    name: 'Hatsune Miku',
    idle: 12,
    // Los cuadros de baile del paquete de Miku vienen cortados: se usan los de idle (completos) y el movimiento es por código.
    dances: [
      { dir: 'idle', frames: 12, style: 'bounce' },
      { dir: 'idle', frames: 12, style: 'sway' },
    ],
    jump: null,
    fx: ['beat_cyan.png', 'beat_heart.png', 'beat_pink.png', 'beat_star.png'],
    flip: true,
    drawSize: 120,
  },
  gir: {
    id: 'gir',
    name: 'GIR',
    idle: 12,
    dances: [
      { dir: 'dance1', frames: 32, style: 'bounce' },
      { dir: 'dance2', frames: 24, style: 'sway' },
    ],
    jump: { dir: 'jump', frames: 12 },
    fx: [],
    flip: false,
    drawSize: 104,
  },
  cat: {
    id: 'cat',
    name: 'Breakdance Cat',
    idle: 8,
    dances: [
      { dir: 'dance', frames: 23, style: 'bounce' },
      { dir: 'dance', frames: 23, style: 'sway', reverse: true },
    ],
    jump: null,
    fx: [],
    flip: false,
    drawSize: 120,
    framed: true,
  },
}

export const spriteUrl = (char: string, dir: string, n: number): string =>
  `./sprites/${char}/${dir}/${String(n).padStart(2, '0')}.png`

export const fxUrl = (char: string, file: string): string => `./sprites/${char}/fx/${file}`
