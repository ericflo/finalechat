import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { writeFileSync } from "node:fs";
import { execSync } from "node:child_process";

function buildId(): string {
  const stamp = new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
  try {
    const sha = execSync("git rev-parse --short=12 HEAD", { stdio: ["ignore", "pipe", "ignore"] }).toString().trim();
    return `${stamp}-${sha}`;
  } catch {
    return stamp;
  }
}

const version = process.env.FINALECHAT_VERSION || buildId();

export default defineConfig({
  plugins: [
    react(),
    {
      // The Go module embeds dist/ and Git tracks an empty marker there so a
      // fresh checkout still compiles; emptyOutDir removes it, so restore it.
      name: "finalechat-keep-marker",
      closeBundle() {
        writeFileSync("../internal/webassets/dist/.gitkeep", "");
      },
    },
  ],
  define: {
    __APP_VERSION__: JSON.stringify(version),
  },
  build: {
    outDir: "../internal/webassets/dist",
    emptyOutDir: true,
    sourcemap: false,
    target: "es2022",
    rollupOptions: {
      input: {
        main: "index.html",
        sw: "sw/sw.ts",
      },
      output: {
        entryFileNames: (chunk) => (chunk.name === "sw" ? "sw.js" : "assets/[name]-[hash].js"),
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://127.0.0.1:8787", changeOrigin: false },
      "/AGENTS.md": "http://127.0.0.1:8787",
      "/install.sh": "http://127.0.0.1:8787",
    },
  },
});
