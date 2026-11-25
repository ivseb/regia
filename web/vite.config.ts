import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// La build finisce dentro il pacchetto Go che la include nel binario (go:embed).
// In sviluppo le chiamate /api vanno al backend locale.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/webui/dist/app",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
});
