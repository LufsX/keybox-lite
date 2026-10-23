import { defineConfig } from "vite"
import { svelte } from "@sveltejs/vite-plugin-svelte"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  base: "./",
  build: {
    outDir: "../webroot",
    emptyOutDir: true,
    target: "es2020",
    minify: "terser",
    terserOptions: { compress: { passes: 2 } }
  }
})
