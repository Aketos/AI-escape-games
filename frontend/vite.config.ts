import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/ws': {
        target: 'ws://localhost:8080',
        ws: true,
      },
      '/scenarios': 'http://localhost:8080',
      '/game-state': 'http://localhost:8080',
      '/api': 'http://localhost:8080',
    },
  },
})
