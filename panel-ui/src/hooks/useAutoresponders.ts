// useAutoresponders.ts — M6.5 Step 3 vacation response hooks.
//
// Wire contract: GET/PUT/DELETE /mailboxes/:mbid/autoresponder
// Verified against panel-api/internal/api/mailbox_autoresponder.go.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../apiClient";

export interface Autoresponder {
  mailbox_id: string;
  enabled: boolean;
  from_date: string | null;
  to_date: string | null;
  subject: string | null;
  text_body: string | null;
  html_body: string | null;
  updated_at: string;
}

export interface AutoresponderInput {
  enabled: boolean;
  from_date?: string | null;
  to_date?: string | null;
  subject?: string | null;
  text_body?: string | null;
  html_body?: string | null;
}

const QK = (mailboxID: string) => ["autoresponder", mailboxID];

export function useAutoresponder(mailboxID: string) {
  return useQuery({
    queryKey: QK(mailboxID),
    queryFn: async () => {
      const { data } = await apiClient.get<Autoresponder>(
        `/mailboxes/${mailboxID}/autoresponder`,
      );
      return data;
    },
    enabled: !!mailboxID,
  });
}

export function useUpdateAutoresponder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ mailboxID, input }: { mailboxID: string; input: AutoresponderInput }) => {
      const { data } = await apiClient.put<Autoresponder>(
        `/mailboxes/${mailboxID}/autoresponder`,
        input,
      );
      return data;
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: QK(data.mailbox_id) });
    },
  });
}

export function useDeleteAutoresponder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (mailboxID: string) => {
      await apiClient.delete(`/mailboxes/${mailboxID}/autoresponder`);
    },
    onSuccess: (_void, mailboxID) => {
      qc.invalidateQueries({ queryKey: QK(mailboxID) });
    },
  });
}
