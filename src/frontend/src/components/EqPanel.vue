<script setup lang="ts">
import { EQ_LABELS, EQ_RANGE, player, resetEq, setEq, setEqOn } from '../player/engine'

function fromPointer(e: PointerEvent, el: HTMLElement): number {
  const r = el.getBoundingClientRect()
  const t = Math.min(1, Math.max(0, (e.clientY - r.top) / r.height))
  const db = (0.5 - t) * 2 * EQ_RANGE
  return Math.round(db * 2) / 2
}

function down(e: PointerEvent, i: number) {
  const el = e.currentTarget as HTMLElement
  el.setPointerCapture(e.pointerId)
  setEq(i, fromPointer(e, el))
  const move = (ev: PointerEvent) => setEq(i, fromPointer(ev, el))
  const up = () => {
    el.removeEventListener('pointermove', move)
    el.removeEventListener('pointerup', up)
    el.removeEventListener('pointercancel', up)
  }
  el.addEventListener('pointermove', move)
  el.addEventListener('pointerup', up)
  el.addEventListener('pointercancel', up)
}

const fillStyle = (db: number) => {
  const half = 50
  const h = (Math.abs(db) / EQ_RANGE) * half
  return db >= 0 ? { bottom: '50%', height: h + '%' } : { top: '50%', height: h + '%' }
}
const knobTop = (db: number) => ((0.5 - db / (2 * EQ_RANGE)) * 100).toFixed(2) + '%'
</script>

<template>
  <div class="panel-box">
    <div class="cap">
      Equalizer
      <span class="sp" />
      <button class="tog" :class="{ on: player.eqOn }" @click="setEqOn(!player.eqOn)">ON</button>
      <button class="tog" @click="resetEq">RESET</button>
    </div>
    <div class="eq" :style="{ opacity: player.eqOn ? 1 : 0.45 }">
      <div class="eq-scale">
        <span>+{{ EQ_RANGE }}db</span>
        <span>0db</span>
        <span>-{{ EQ_RANGE }}db</span>
      </div>
      <div v-for="(l, i) in EQ_LABELS" :key="l" class="eq-band">
        <div class="eq-track" @pointerdown.prevent="down($event, i)" @dblclick="setEq(i, 0)">
          <div class="eq-fill" :style="fillStyle(player.eq[i])" />
          <div class="eq-mid" />
          <div class="eq-knob" :style="{ top: knobTop(player.eq[i]) }" />
        </div>
        <label>{{ l }}</label>
      </div>
    </div>
  </div>
</template>
