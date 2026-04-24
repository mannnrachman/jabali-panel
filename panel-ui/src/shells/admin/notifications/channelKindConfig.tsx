// channelKindConfig.tsx — per-kind form fields + palette for the M14
// notification channel drawer. Keeps the dynamic form code in
// AdminChannelDrawer.tsx short; adding a new channel kind is a diff to
// this file only.

export type ChannelKind =
  | "email"
  | "slack"
  | "discord"
  | "ntfy"
  | "webhook"
  | "webpush";

export const CHANNEL_KINDS: ChannelKind[] = [
  "email",
  "slack",
  "discord",
  "ntfy",
  "webhook",
  "webpush",
];

export const kindColors: Record<ChannelKind, string> = {
  email: "blue",
  slack: "purple",
  discord: "geekblue",
  ntfy: "cyan",
  webhook: "gold",
  webpush: "green",
};

export const kindLabels: Record<ChannelKind, string> = {
  email: "Email",
  slack: "Slack",
  discord: "Discord",
  ntfy: "ntfy.sh",
  webhook: "Generic webhook",
  webpush: "Web Push (browser)",
};

// ChannelFormFields — which per-kind config fields to render. The
// drawer renders inputs in this order. Each field is bound to the
// NotificationChannelConfig JSON blob on the row.
export type FieldSpec = {
  name: keyof ChannelFormConfig;
  label: string;
  placeholder?: string;
  type?: "text" | "number" | "password" | "tags";
  required?: boolean;
  help?: string;
};

export type ChannelFormConfig = {
  url?: string;
  bearer?: string;
  hmac_secret?: string;
  priority?: number;
  tags?: string[];
  to_email?: string;
  from_email?: string;
};

export const kindFields: Record<ChannelKind, FieldSpec[]> = {
  email: [
    { name: "to_email", label: "Recipient", placeholder: "admin@example.com", required: true },
    { name: "from_email", label: "Envelope sender", placeholder: "jabali@example.com", required: true },
  ],
  slack: [
    { name: "url", label: "Webhook URL", placeholder: "https://hooks.slack.com/services/…", required: true },
  ],
  discord: [
    { name: "url", label: "Webhook URL", placeholder: "https://discord.com/api/webhooks/…", required: true },
  ],
  ntfy: [
    { name: "url", label: "Topic URL", placeholder: "https://ntfy.sh/your-topic", required: true },
    { name: "bearer", label: "Bearer token (optional)", type: "password", help: "Sent as Authorization: Bearer …" },
    { name: "priority", label: "Priority (1–5, optional)", type: "number" },
    { name: "tags", label: "Tags (optional)", type: "tags", help: "Comma-separated, e.g. warning,fire" },
  ],
  webhook: [
    { name: "url", label: "Target URL", placeholder: "https://example.com/hooks/jabali", required: true },
    {
      name: "hmac_secret",
      label: "HMAC secret",
      type: "password",
      required: true,
      help: "≥16 chars. Shared with the receiver, used to sign X-Jabali-Signature.",
    },
  ],
  webpush: [],
};
