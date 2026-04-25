// Support links — placeholders until product gives final URLs. When a
// const is empty, SupportPage hides the corresponding CTA button rather
// than ship a dead link.
export const SUPPORT_LINKS = {
  documentation: "https://docs.jabali-panel.io",
  githubIssues: "https://github.com/jabali-team/jabali-panel/issues",
  paidSupport: "https://jabali-panel.io/support",
  emergency: "mailto:emergency@jabali-panel.io",
} as const;

// Where the diagnostic-report email is addressed. Operator clicks
// "Send via email" → mail client opens with this recipient + a
// pre-filled subject/body containing the encrypted-note URL + password.
export const DIAGNOSTIC_EMAIL_RECIPIENT = "support@jabali-panel.com";
