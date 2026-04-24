package eventsources

// Backup failure events source.
//
// TODO(M15): once the backup subsystem lands, subscribe to its outcome
// channel and fire `backup.fail` envelopes on non-zero exits. Envelope
// body should include the job id + stdout tail so the admin can
// diagnose from the bell without paging through systemd logs.
