import { defineConfig } from 'vite'
import path from 'path'
import { fileURLToPath } from 'url'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

const dirname = path.dirname(fileURLToPath(import.meta.url))

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: '127.0.0.1',
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(dirname, './src'),
      '@flashit': path.resolve(dirname, './bindings/github.com/kyleaupton/flashit/internal'),
    },
  },
})
