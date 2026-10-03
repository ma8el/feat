<script setup lang="ts">
// Lanes grow left to right and branch the way the logo's lanes leave its beam:
// a diagonal step to another row, then straight on, with a cursor block ahead of the tip.
type Point = { x: number, y: number }
type Lane = { points: Point[], length: number, total: number, speed: number, amber: boolean, fade: number }

const canvas = ref<HTMLCanvasElement>()

const ROW = 28
const GAP = 4
const BLOCK = 7
const MAX_LANES = 26

onMounted(() => {
  const el = canvas.value!
  const ctx = el.getContext('2d')!
  let width = 0
  let height = 0
  let lanes: Lane[] = []

  function resize() {
    const dpr = window.devicePixelRatio || 1
    width = el.clientWidth
    height = el.clientHeight
    el.width = width * dpr
    el.height = height * dpr
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  }

  const rows = () => Math.max(1, Math.floor(height / ROW))
  const span = (points: Point[]) => points.slice(1).reduce((sum, p, i) => sum + Math.hypot(p.x - points[i]!.x, p.y - points[i]!.y), 0)

  function lane(points: Point[], amber: boolean): Lane {
    const end = points.at(-1)!
    const run = end.x + 120 + Math.random() * width * 0.6
    points.push({ x: Math.min(run, width + 40), y: end.y })
    return { points, length: 0, total: span(points), speed: 50 + Math.random() * 70, amber, fade: 1 }
  }

  function spawnRoot() {
    const y = (1 + Math.floor(Math.random() * rows())) * ROW
    const x = -20 + Math.random() * width * 0.5
    lanes.push(lane([{ x, y }, { x: x + 1, y }], Math.random() < 0.16))
  }

  // A branch leaves its parent's tip like the logo's lanes: one diagonal step, then straight.
  function spawnBranch(parent: Lane, at: Point) {
    const step = (Math.random() < 0.5 ? -1 : 1) * (1 + Math.floor(Math.random() * 2)) * ROW
    const y = at.y + step
    if (y < ROW || y > height - ROW) return
    lanes.push(lane([{ ...at }, { x: at.x + Math.abs(step) * 0.73, y }], false))
  }

  function tip(l: Lane): { at: Point, dir: Point } {
    let left = l.length
    for (let i = 1; i < l.points.length; i++) {
      const a = l.points[i - 1]!
      const b = l.points[i]!
      const d = Math.hypot(b.x - a.x, b.y - a.y)
      if (left <= d) {
        const t = d ? left / d : 0
        return { at: { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t }, dir: { x: (b.x - a.x) / d, y: (b.y - a.y) / d } }
      }
      left -= d
    }
    return { at: l.points.at(-1)!, dir: { x: 1, y: 0 } }
  }

  function step(dt: number) {
    for (const l of lanes) {
      if (l.length < l.total) {
        l.length = Math.min(l.total, l.length + l.speed * dt)
        const { at, dir } = tip(l)
        if (dir.y === 0 && lanes.length < MAX_LANES && Math.random() < dt * 0.25) spawnBranch(l, at)
      }
      else {
        l.fade -= dt * 0.35
      }
    }
    lanes = lanes.filter(l => l.fade > 0)
    if (lanes.length < MAX_LANES / 2 && Math.random() < dt * 1.5) spawnRoot()
  }

  function draw(time: number) {
    ctx.clearRect(0, 0, width, height)
    ctx.lineCap = 'round'
    ctx.lineJoin = 'round'
    ctx.lineWidth = 2
    const blink = Math.floor(time / 530) % 2 === 0
    for (const l of lanes) {
      const colour = l.amber ? `rgba(245, 166, 35, ${0.45 * l.fade})` : `rgba(233, 237, 247, ${0.09 * l.fade})`
      const { at } = tip(l)
      ctx.strokeStyle = colour
      ctx.beginPath()
      ctx.moveTo(l.points[0]!.x, l.points[0]!.y)
      let left = l.length
      for (let i = 1; i < l.points.length && left > 0; i++) {
        const a = l.points[i - 1]!
        const b = l.points[i]!
        const d = Math.hypot(b.x - a.x, b.y - a.y)
        if (left >= d) ctx.lineTo(b.x, b.y)
        else ctx.lineTo(at.x, at.y)
        left -= d
      }
      ctx.stroke()
      // A growing lane's cursor blinks like a terminal's; a finished one holds still while it fades.
      if (l.length >= l.total || blink) {
        ctx.fillStyle = colour
        ctx.fillRect(at.x + GAP, at.y - BLOCK / 2, BLOCK, BLOCK)
      }
    }
  }

  resize()
  for (let i = 0; i < 8; i++) spawnRoot()

  // Motion is decoration: visitors who ask for less of it get one settled frame.
  const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  // Resizing a canvas clears it, so a still frame has to be drawn again.
  const observer = new ResizeObserver(() => { resize(); if (still) draw(0) })
  observer.observe(el)
  if (still) {
    for (let i = 0; i < 400; i++) step(1 / 30)
    draw(0)
    onBeforeUnmount(() => observer.disconnect())
    return
  }

  let last = performance.now()
  let frame = requestAnimationFrame(function loop(now) {
    step(Math.min(0.05, (now - last) / 1000))
    last = now
    draw(now)
    frame = requestAnimationFrame(loop)
  })
  onBeforeUnmount(() => {
    cancelAnimationFrame(frame)
    observer.disconnect()
  })
})
</script>

<template>
  <div class="pointer-events-none absolute inset-0 -z-10 overflow-hidden" aria-hidden="true">
    <canvas ref="canvas" class="size-full [mask-image:radial-gradient(ellipse_at_top,black_30%,transparent_75%)]" />
    <div class="absolute inset-x-0 top-1/4 mx-auto h-72 max-w-3xl rounded-full bg-primary/10 blur-3xl" />
  </div>
</template>
