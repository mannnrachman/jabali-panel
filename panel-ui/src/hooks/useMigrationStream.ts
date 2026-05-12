// useMigrationStream — ADR-0095 decision 4 SSE wrapper.
//
// Opens an EventSource on /api/v1/admin/migrations/:id/stream and
// surfaces every "snapshot" event as { job, stages } state. Backend
// closes the connection automatically once the job hits a terminal
// state; this hook also closes on unmount or when the id changes.
//
// Falls back to a one-shot REST fetch if the browser blocks EventSource
// (e.g. service worker quirk). The polling refetchInterval pattern
// elsewhere in the SPA still works for callers that need it; this hook
// is opt-in for views that want smooth updates.
import { useEffect, useState } from "react";

import { apiClient } from "../apiClient";

export type MigrationJob = {
  id: string;
  batch_id: string | null;
  source_kind: string;
  source_host: string;
  source_user: string;
  state: string;
  target_user_id: string | null;
  manifest_json: string | null;
  last_error: string | null;
  started_at: string;
  ended_at: string | null;
  created_at: string;
  updated_at: string;
};

export type MigrationStage = {
  id: string;
  job_id: string;
  stage_name: string;
  state: string;
  started_at: string | null;
  ended_at: string | null;
  bytes_processed: number;
  last_error: string | null;
  created_at: string;
  updated_at: string;
};

export type MigrationSnapshot = {
  job: MigrationJob;
  stages: MigrationStage[];
};

const TERMINAL = new Set(["done", "failed", "cancelled"]);

export function useMigrationStream(id: string | null): {
  data: MigrationSnapshot | null;
  error: string | null;
  loading: boolean;
} {
  const [data, setData] = useState<MigrationSnapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(true);

  useEffect(() => {
    if (!id) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);

    // Same-origin SSE — vite preview / nginx proxy both forward
    // /api/v1/* to panel-api. EventSource sends the Kratos session
    // cookie automatically.
    const url = `/api/v1/admin/migrations/${id}/stream`;
    let es: EventSource | null = null;
    let cancelled = false;

    try {
      es = new EventSource(url, { withCredentials: true });

      es.addEventListener("snapshot", (ev) => {
        if (cancelled) return;
        try {
          const snap = JSON.parse((ev as MessageEvent).data) as MigrationSnapshot;
          setData(snap);
          setLoading(false);
          if (TERMINAL.has(snap.job.state)) {
            es?.close();
          }
        } catch (e) {
          setError(e instanceof Error ? e.message : "parse error");
        }
      });

      es.addEventListener("error", () => {
        if (cancelled) return;
        // EventSource auto-reconnects on transient drops; only flip
        // the error state once we've gone three reconnect cycles
        // without a snapshot. The browser exposes that via
        // readyState=CLOSED.
        if (es?.readyState === EventSource.CLOSED) {
          setError("stream closed");
        }
      });
    } catch (e) {
      // EventSource ctor threw — fall back to single REST fetch so
      // the page renders something instead of an empty Spin.
      void apiClient
        .get<MigrationSnapshot>(`/admin/migrations/${id}`)
        .then((r) => {
          if (!cancelled) {
            setData(r.data);
            setLoading(false);
          }
        })
        .catch((err: Error) => {
          if (!cancelled) {
            setError(err.message);
            setLoading(false);
          }
        });
    }

    return () => {
      cancelled = true;
      es?.close();
    };
  }, [id]);

  return { data, error, loading };
}
