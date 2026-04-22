// Crypto-random password generator for every password-entry UI in the
// panel. Shared by the admin user-create/edit flow, mailbox create,
// database-user create, and application-install (WordPress etc.).
//
// Design notes:
//   * Uses window.crypto.getRandomValues — not Math.random. Non-crypto
//     PRNGs are predictable from a single observed password; crypto is
//     mandatory for anything a user will actually depend on.
//   * Skips visually ambiguous characters: 0/O, 1/l/I. A user reading
//     a reveal-once password off screen should never have to guess.
//   * Guarantees one char from each class (lower, upper, digit, symbol)
//     — matches the validation rules most of our backend endpoints
//     enforce (users.create minimum 10, mailbox minimum 8, etc.).
//   * Symbol set intentionally narrow: shell-safe characters only, no
//     quotes, backticks, backslashes, spaces or `$`. The panel does not
//     escape passwords when rendering CLI snippets; this constraint
//     keeps those snippets copy-pasteable.

const LOWERCASE = "abcdefghijkmnopqrstuvwxyz"; // no l
const UPPERCASE = "ABCDEFGHJKLMNPQRSTUVWXYZ"; // no I, O
const DIGITS = "23456789"; // no 0, 1
const SYMBOLS = "!@#%^&*-_=+?";

export function generatePassword(length = 16): string {
  if (length < 4) {
    throw new Error("generatePassword: length must be >= 4 (one of each class)");
  }

  const all = LOWERCASE + UPPERCASE + DIGITS + SYMBOLS;
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);

  const chars: string[] = [
    LOWERCASE[bytes[0] % LOWERCASE.length],
    UPPERCASE[bytes[1] % UPPERCASE.length],
    DIGITS[bytes[2] % DIGITS.length],
    SYMBOLS[bytes[3] % SYMBOLS.length],
  ];
  for (let i = 4; i < length; i++) {
    chars.push(all[bytes[i] % all.length]);
  }

  // Fisher-Yates shuffle with a fresh batch of crypto random bytes so
  // the guaranteed-class chars aren't always in positions 0..3.
  const shuffleBytes = new Uint8Array(length);
  crypto.getRandomValues(shuffleBytes);
  for (let i = length - 1; i > 0; i--) {
    const j = shuffleBytes[i] % (i + 1);
    [chars[i], chars[j]] = [chars[j], chars[i]];
  }

  return chars.join("");
}
