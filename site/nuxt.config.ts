// The Pages workflow sets NUXT_APP_BASE_URL and NUXT_SITE_URL; Docus reads the site URL only from the environment.
export default defineNuxtConfig({
  extends: ['docus'],
  site: {
    name: 'Feat',
  },
  // GitHub Pages serves files only, so there is no image server to resize through.
  image: {
    provider: 'none',
  },
  // Crawlers read robots.txt only at the host root, which a project site does not own.
  robots: {
    robotsTxt: false,
  },
  // nuxt-og-image 6.7.8 puts the base path into the image URL twice under a site URL with a path.
  ogImage: {
    enabled: false,
  },
  nitro: {
    prerender: {
      // Docus writes page.html beside a page/ folder of payloads; GitHub Pages needs page/index.html.
      autoSubfolderIndex: true,
      // llms-full.txt fails in Docus 5.13.0 on any bold text, the stock starter included.
      routes: ['/llms.txt', '/llms-full.txt'],
    },
  },
})
