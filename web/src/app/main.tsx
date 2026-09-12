import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import { App } from "./App";
import { setTokenProvider } from "@/shared/api";
import { getStoredToken } from "./session";
import "./styles.css";

// token 只存内存 + sessionStorage（不落 localStorage，降低 XSS 影响面）
setTokenProvider(getStoredToken);

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // 画布相关数据以 SSE 为权威，轮询只作为兜底
      staleTime: 5_000,
      retry: (failureCount, error) => {
        const status = (error as { status?: number }).status;
        if (status && status >= 400 && status < 500) return false;
        return failureCount < 2;
      },
    },
  },
});

const root = document.getElementById("root");
if (!root) throw new Error("#root not found");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
