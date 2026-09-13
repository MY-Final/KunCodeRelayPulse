import path from "node:path"
import { fileURLToPath } from "node:url"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

const frontendRoot = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/static/",
  resolve: {
    alias: {
      "@": path.resolve(frontendRoot, "src"),
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:18080",
      "/ready": "http://127.0.0.1:18080",
    },
  },
  build: {
    outDir: path.resolve(frontendRoot, "../internal/web/static"),
    emptyOutDir: true,
  },
})
