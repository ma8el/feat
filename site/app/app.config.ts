export default defineAppConfig({
  // The hero's grid, glows, and terminal frame are designed for a dark page only.
  docus: {
    colorMode: 'dark',
  },
  seo: {
    title: 'Feat',
    description: 'A terminal control plane for running feature work through several coding-agent sessions in parallel.',
  },
  header: {
    title: 'Feat',
    logo: {
      light: '/assets/feat-wordmark-light.svg',
      dark: '/assets/feat-wordmark-dark.svg',
      alt: 'Feat',
      // The wordmark's viewBox pads its letters top and bottom, so h-6 draws them at about 12px.
      class: 'h-10',
    },
  },
  github: {
    url: 'https://github.com/ma8el/feat',
    branch: 'main',
    rootDir: 'site',
  },
  ui: {
    colors: {
      // Closest Tailwind palette to the logo's #f5a623; see docs/assets/README.md.
      primary: 'amber',
      neutral: 'zinc',
    },
  },
})
