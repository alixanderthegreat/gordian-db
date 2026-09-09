import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:7474',
        changeOrigin: true,
      },
    },
  },
  build: {
    // Builds directly into the graphui package (kata cycle 52) so go:embed can pick it up with no
    // separate copy step - the built UI is a real part of that importable Go package now, not a
    // loose directory referenced by path.
    outDir: '../graphui/dist',
    emptyOutDir: true,
  },
})
