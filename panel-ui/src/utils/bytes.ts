// Human-readable byte formatter shared across panel components.
//
// Three duplicates lived in MyProfileUsageCard, MyProfileBackupCard,
// and the original M13.1 column. This is the canonical home.

export function humanBytes(b: number | null | undefined): string {
  if (b === null || b === undefined || b <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  let n = b;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return i === 0 ? `${Math.floor(n)} B` : `${n.toFixed(1)} ${units[i]}`;
}
