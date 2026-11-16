import type { RuntimeSDK } from "./runtime-types";

export const featureSDK: RuntimeSDK = {
  async submitForm(featureKey, data) {
    const response = await fetch(`/api/v2/forms/${encodeURIComponent(featureKey)}/submissions`, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ data }),
    });
    if (!response.ok) {
      const body = await response.json().catch(() => null) as { error?: { message?: string }; message?: string } | null;
      throw new Error(body?.error?.message ?? body?.message ?? `Form request failed (${response.status})`);
    }
    return response.json() as Promise<{ id: string; status: string }>;
  },
};
