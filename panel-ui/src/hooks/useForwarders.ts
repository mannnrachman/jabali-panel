// useForwarders.ts — M6.5 Step 5 forwarder hooks.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../apiClient";

export interface Forwarder {
  id: string;
  mailbox_id: string;
  mailbox_email: string;
  domain_id: string;
  domain_name: string;
  type: "alias" | "external";
  local_part?: string;
  target: string;
  enabled: boolean;
  created_at: string;
}

const QK = ["forwarders"];

export function useForwarders() {
  return useQuery({
    queryKey: QK,
    queryFn: async () => {
      const { data } = await apiClient.get<{ data: Forwarder[]; total: number }>("/mail/forwarders");
      return data.data ?? [];
    },
  });
}

export function useCreateForwarder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      mailboxID,
      type,
      localPart,
      target,
    }: {
      mailboxID: string;
      type: "alias" | "external";
      localPart?: string;
      target: string;
    }) => {
      const { data } = await apiClient.post<Forwarder>(
        `/mailboxes/${mailboxID}/forwarders`,
        { type, local_part: localPart, target },
      );
      return data;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK }),
  });
}

export function useDeleteForwarder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (forwarderID: string) => {
      await apiClient.delete(`/forwarders/${forwarderID}`);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK }),
  });
}
