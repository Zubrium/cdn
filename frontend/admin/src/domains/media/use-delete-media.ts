import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/api";
import { errorMessage } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

export interface DeleteMediaInput {
  id: string;
  revision: number;
}

/** One destructive media mutation for every gallery surface. */
export function useDeleteMedia(onDeleted?: () => void) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, revision }: DeleteMediaInput) => api.media.remove(id, revision),
    onSuccess: () => {
      toast.success("Файл удалён");
      void queryClient.invalidateQueries({ queryKey: queryKeys.media.all });
      onDeleted?.();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
}
