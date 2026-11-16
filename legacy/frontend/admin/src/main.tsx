import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import { Toaster } from "sonner";
import { ThemeProvider, useTheme } from "@/app/theme";
import { AuthProvider } from "@/app/auth";
import { router } from "@/app/router";
import { ConfirmProvider } from "@/components/common/confirm";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ApiError } from "@/api";
import "@website-engine/ui/styles/foundation.css";
import "@website-engine/ui/styles/admin.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 20_000,
      refetchOnWindowFocus: false,
      retry: (failureCount, error) => {
        if (error instanceof ApiError) {
          if ([400, 401, 403, 404, 409, 422].includes(error.status)) return false;
        }
        return failureCount < 2;
      },
    },
    mutations: {
      retry: false,
    },
  },
});

function ThemedToaster() {
  const { resolved } = useTheme();
  return (
    <Toaster
      theme={resolved}
      position="top-right"
      closeButton
      richColors
      toastOptions={{ duration: 5000 }}
    />
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <TooltipProvider delayDuration={250}>
          <AuthProvider>
            <ConfirmProvider>
              <RouterProvider router={router} future={{ v7_startTransition: true }} />
              <ThemedToaster />
            </ConfirmProvider>
          </AuthProvider>
        </TooltipProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
