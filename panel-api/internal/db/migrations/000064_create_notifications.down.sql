-- Reverse order — drop FK-referencing tables before their targets.
-- notification_channels is referenced by both webhook_endpoints and
-- notification_history, so it drops last.
DROP TABLE IF EXISTS webpush_subscriptions;
DROP TABLE IF EXISTS notification_history;
DROP TABLE IF EXISTS webhook_endpoints;
DROP TABLE IF EXISTS notification_channels;
