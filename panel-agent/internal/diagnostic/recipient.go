package diagnostic

// RecipientPublicKey is the age recipient diagnostic reports are encrypted
// to. The matching private key is held off-host by the Jabali team and
// NEVER appears on a managed install.
//
// Rotation: bump the constant, tag a release, ship via `jabali update`.
// Old ciphertexts remain decryptable as long as the team retains the old
// private key (long-lived, encrypted at rest in their password manager).
//
// PLACEHOLDER: this is a generated test recipient. The real one will be
// swapped in via a follow-up commit (ADR-0064 §rotation). Operators
// running diagnostics today still get encryption — just to a key the
// team will replace before the public release.
const RecipientPublicKey = "age13trnrev8dmdva5tsjnhnmdrlpnukl5d47rjerv72jem90ucqtyasdzwts0"
