// Support links. SupportPage hides any CTA whose value is empty, so
// removing one here gracefully degrades to a "Coming soon" tag rather
// than a dead link.
export const SUPPORT_LINKS = {
  documentation: "https://docs.jabali-panel.com",
  githubIssues: "https://github.com/jabali-panel/jabali/issues",
  paidSupport: "https://jabali-panel.com/support",
  emergency: "mailto:webmaster@jabali-panel.com?subject=URGENT%3A%20Jabali%20Panel%20incident",
} as const;

// Where the diagnostic-report email is addressed. Operator clicks
// "Send via email" → mail client opens with this recipient + a
// pre-filled subject/body containing the encrypted-note URL + password.
export const DIAGNOSTIC_EMAIL_RECIPIENT = "webmaster@jabali-panel.com";
