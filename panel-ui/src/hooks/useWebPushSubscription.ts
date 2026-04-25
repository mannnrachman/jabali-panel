// useWebPushSubscription — encapsulates the VAPID-key fetch, service
// worker registration, and browser push subscription lifecycle. Mount
// once per shell (NotificationBell does this) and the hook manages
// all the navigator plumbing.
//
// On an unsupported browser (no ServiceWorker or PushManager) the hook
// reports `supported=false` and every action short-circuits. The
// NotificationBell handles the UI downgrade.
import { useCallback, useEffect, useMemo, useState } from "react";

import { apiClient } from "../apiClient";

// Service worker path. Matches the file under panel-ui/public.
const SW_PATH = "/sw-push.js";
const SW_SCOPE = "/";

export type PushPermission = NotificationPermission | "unsupported";

type VAPIDResponse = { public_key: string };

function base64UrlToUint8Array(base64url: string): Uint8Array {
  const padding = "=".repeat((4 - (base64url.length % 4)) % 4);
  const base64 = (base64url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = window.atob(base64);
  const arr = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i);
  return arr;
}

function ab2b64Url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return window.btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export interface WebPushState {
  supported: boolean;
  permission: PushPermission;
  subscribed: boolean;
  loading: boolean;
  subscribe: () => Promise<void>;
  unsubscribe: () => Promise<void>;
  // Service worker registration — exposed so consumers can use it
  // directly (e.g. for navigator.permissions query fallbacks).
  registration?: ServiceWorkerRegistration | null;
}

export function useWebPushSubscription(): WebPushState {
  const supported = useMemo(() => {
    // Chrome refuses to register a ServiceWorker on an origin that
    // isn't a secure context — and an origin served with a self-signed
    // or cert-error TLS session is NOT a secure context even after the
    // user clicks through the warning page. Skip registration entirely
    // so the console doesn't fill with SecurityError stack traces and
    // the bell's downgrade UI kicks in cleanly.
    if (typeof window === "undefined") return false;
    if (!window.isSecureContext) return false;
    return (
      "serviceWorker" in navigator &&
      "PushManager" in window &&
      "Notification" in window
    );
  }, []);

  const [registration, setRegistration] = useState<ServiceWorkerRegistration | null>(null);
  const [subscribed, setSubscribed] = useState(false);
  const [permission, setPermission] = useState<PushPermission>(supported ? Notification.permission : "unsupported");
  const [loading, setLoading] = useState(false);

  // Register the service worker on first mount. Only once per tab;
  // navigator.serviceWorker.register is idempotent on the same URL.
  //
  // After register() we await navigator.serviceWorker.ready instead of
  // calling pushManager.getSubscription() on the freshly-returned
  // registration. register() resolves as soon as the SW file is fetched
  // and parsed, before the worker reaches the activated state — and on
  // Firefox in particular, querying the push manager during the
  // installing phase can return null even when a real subscription
  // exists from a previous session, which is the symptom we hit:
  // refresh flipped the bell back to "Enable" when it should stay
  // "Disable".
  useEffect(() => {
    if (!supported) return;
    let cancelled = false;
    (async () => {
      try {
        await navigator.serviceWorker.register(SW_PATH, { scope: SW_SCOPE });
        const ready = await navigator.serviceWorker.ready;
        if (cancelled) return;
        setRegistration(ready);
        const existing = await ready.pushManager.getSubscription();
        if (cancelled) return;
        setSubscribed(existing !== null);
      } catch (err) {
        // Don't blow up the UI — bell just shows the disabled state.
        // Logged at console level so devs see it in dev tools without
        // spamming production error telemetry.
        console.warn("[webpush] service worker register failed", err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [supported]);

  const subscribe = useCallback(async () => {
    if (!supported || !registration) return;
    setLoading(true);
    try {
      const perm = await Notification.requestPermission();
      setPermission(perm);
      if (perm !== "granted") return;

      const { data } = await apiClient.get<VAPIDResponse>(
        "/notifications/webpush/vapid-public-key",
      );
      const serverKey = base64UrlToUint8Array(data.public_key);
      const sub = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: serverKey.buffer.slice(
          serverKey.byteOffset,
          serverKey.byteOffset + serverKey.byteLength,
        ) as ArrayBuffer,
      });
      const keys = sub.toJSON().keys ?? {};
      const p256dh = keys.p256dh ?? ab2b64Url(sub.getKey("p256dh") ?? new ArrayBuffer(0));
      const auth = keys.auth ?? ab2b64Url(sub.getKey("auth") ?? new ArrayBuffer(0));
      await apiClient.post("/notifications/webpush/subscribe", {
        endpoint: sub.endpoint,
        keys: { p256dh, auth },
        user_agent: navigator.userAgent,
      });
      setSubscribed(true);
    } catch (err) {
      console.warn("[webpush] subscribe failed", err);
    } finally {
      setLoading(false);
    }
  }, [registration, supported]);

  const unsubscribe = useCallback(async () => {
    if (!supported || !registration) return;
    setLoading(true);
    try {
      const sub = await registration.pushManager.getSubscription();
      if (!sub) {
        setSubscribed(false);
        return;
      }
      const endpoint = sub.endpoint;
      await sub.unsubscribe();
      await apiClient.delete("/notifications/webpush/subscribe", { data: { endpoint } });
      setSubscribed(false);
    } catch (err) {
      console.warn("[webpush] unsubscribe failed", err);
    } finally {
      setLoading(false);
    }
  }, [registration, supported]);

  return { supported, permission, subscribed, loading, subscribe, unsubscribe, registration };
}
