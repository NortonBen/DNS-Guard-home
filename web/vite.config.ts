import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath, URL } from 'node:url';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    // Xuất thẳng vào package Go để binary nhúng được giao diện. Một binary duy nhất
    // nghĩa là không bao giờ có chuyện giao diện lệch phiên bản so với API.
    outDir: '../internal/web/assets',
    emptyOutDir: true,
    // Ngân sách bundle là 300 KB gzip; cảnh báo sớm hơn ngưỡng đó để còn kịp xử lý.
    chunkSizeWarningLimit: 700,
    rollupOptions: {
      output: {
        manualChunks: {
          // Recharts nặng và chỉ dùng ở dashboard; tách riêng để các trang khác
          // không phải tải nó.
          charts: ['recharts'],
        },
      },
    },
  },
  server: {
    port: 5173,
    // Dev server gọi thẳng backend, nên không cần CORS và cookie phiên hoạt động
    // như khi chạy thật.
    proxy: {
      '/api': 'http://localhost:8080',
      '/lists': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
    },
  },
});
