import { writeFileSync } from "node:fs";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// `npm run dev` proxies the API to `sbx-projects api` on this machine, whose
// certificate is self-signed.
const api = process.env.SBXP_API ?? "https://localhost:7443";
const outDir = "../internal/webui/dist";

export default defineConfig({
  plugins: [
    react(),
    {
      // The build empties the folder Go embeds; .keep is what lets it
      // compile before the first build.
      name: "keep-dist",
      closeBundle() {
        writeFileSync(`${outDir}/.keep`, "");
      },
    },
  ],
  build: { outDir, emptyOutDir: true },
  server: {
    host: true,
    proxy: { "/v1": { target: api, secure: false, changeOrigin: true, ws: true } },
  },
});
