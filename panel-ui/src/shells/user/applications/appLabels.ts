// Display labels for app_type values shown in the Applications list.
// Falling back to the raw app_type when missing keeps an unknown app
// (e.g. an in-flight rollout where the UI is older than the API)
// readable instead of blank.
export const APP_TYPE_LABELS: Record<string, string> = {
  wordpress: "WordPress",
  dokuwiki: "DokuWiki",
  mediawiki: "MediaWiki",
  drupal: "Drupal",
  joomla: "Joomla",
  phpbb: "phpBB",
};
