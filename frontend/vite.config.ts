import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/v1": "http://localhost:8080",
      "/project": "http://localhost:8080",
      "/health": "http://localhost:8080",
      "/otel": "http://localhost:8080",
    },
  },
  build: {
    sourcemap: false,
    target: "es2022",
  },
});
