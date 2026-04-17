import type { CrudFilters } from "@refinedev/core";

/**
 * Extract the current value of the `q` field from Refine's filter state.
 * Used by list pages to seed SearchableTable's input on first render so a
 * deep-link like `/users?filters[0][field]=q&filters[0][value]=foo` shows
 * "foo" in the box. Returns empty string when no q filter is present.
 */
export function readQValue(filters: CrudFilters | undefined): string {
  if (!filters) return "";
  for (const f of filters) {
    // LogicalFilter has `field`; ConditionalFilter doesn't.
    if ("field" in f && f.field === "q" && f.value != null) {
      return String(f.value);
    }
  }
  return "";
}
