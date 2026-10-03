// Docus's app.vue links /favicon.ico without the base path, which 404s under /feat/.
// Dropping that tag leaves the icons nuxt.config.ts declares.
export default defineNuxtPlugin(() => {
  injectHead().hooks.hook('tags:resolve', (ctx) => {
    ctx.tags = ctx.tags.filter(tag => !(tag.tag === 'link' && tag.props.href === '/favicon.ico'))
  })
})
