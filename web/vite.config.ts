import { defineConfig, type ProxyOptions } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'
import { isIP } from 'node:net'

function isLoopback(address: string) {
  const ip = address.startsWith('::ffff:') ? address.slice(7) : address
  return ip === '::1' || (isIP(ip) === 4 && ip.startsWith('127.'))
}

function localProxy(): ProxyOptions {
  return {
    target: 'http://127.0.0.1:51873',
    changeOrigin: true,
    xfwd: false,
    bypass(req) {
      // Validate the original request before replacing its Host and Origin.
      if (!isLoopback(req.socket.remoteAddress || '') || !req.headers.host) return false
      let origin: URL
      try { origin = new URL(`http://${req.headers.host}`) }
      catch { return false }
      const hostname = origin.hostname.replace(/^\[|\]$/g, '')
      if (origin.host.toLowerCase() !== req.headers.host.toLowerCase() ||
          origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash ||
          (hostname !== 'localhost' && !isLoopback(hostname))) return false
      if (req.headers.origin !== undefined && req.headers.origin !== origin.origin) return false
    },
    configure(proxy, options) {
      const targetOrigin = new URL(options.target as string).origin
      proxy.on('proxyReq', (outgoing, incoming) => {
        // bypass accepted this exact dev origin; absence stays absent.
        if (incoming.headers.origin) outgoing.setHeader('Origin', targetOrigin)
      })
    },
  }
}

// The Go backend embeds this single application from internal/app/web/dist.
export default defineConfig({
  plugins: [vue()],
  base: '/',
  build: {
    outDir: resolve(__dirname, '../internal/app/web/dist'),
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'index.html'),
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': localProxy(),
      '/proxy': localProxy(),
      '/d': localProxy(),
    },
  },
})
