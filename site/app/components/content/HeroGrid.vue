<script setup lang="ts">
const canvas = ref<HTMLCanvasElement>()

onMounted(() => {
  const el = canvas.value!
  const ctx = el.getContext('2d')!
  const cell = 18
  const size = 12
  let cols = 0
  let rows = 0
  let alpha: Float32Array
  // Cubing skews most cells toward dark, so only a few light up.
  const shade = () => Math.random() ** 3 * 0.22

  function resize() {
    const dpr = window.devicePixelRatio || 1
    el.width = el.clientWidth * dpr
    el.height = el.clientHeight * dpr
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    cols = Math.ceil(el.clientWidth / cell)
    rows = Math.ceil(el.clientHeight / cell)
    alpha = Float32Array.from({ length: cols * rows }, shade)
  }

  function draw() {
    ctx.clearRect(0, 0, el.clientWidth, el.clientHeight)
    for (let i = 0; i < alpha.length; i++) {
      ctx.fillStyle = `rgba(245, 166, 35, ${alpha[i]})`
      ctx.fillRect((i % cols) * cell, Math.floor(i / cols) * cell, size, size)
    }
  }

  resize()
  draw()
  const observer = new ResizeObserver(() => { resize(); draw() })
  observer.observe(el)

  // Twinkling is decoration, so it stops for visitors who ask for reduced motion.
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    onBeforeUnmount(() => observer.disconnect())
    return
  }
  const timer = setInterval(() => {
    for (let n = 0; n < alpha.length / 40; n++) {
      alpha[Math.floor(Math.random() * alpha.length)] = shade()
    }
    draw()
  }, 120)
  onBeforeUnmount(() => { clearInterval(timer); observer.disconnect() })
})
</script>

<template>
  <div class="pointer-events-none absolute inset-0 -z-10 hidden overflow-hidden dark:block" aria-hidden="true">
    <canvas ref="canvas" class="size-full [mask-image:radial-gradient(ellipse_at_top,black_30%,transparent_75%)]" />
    <div class="absolute inset-x-0 top-1/4 mx-auto h-72 max-w-3xl rounded-full bg-primary/10 blur-3xl" />
  </div>
</template>
