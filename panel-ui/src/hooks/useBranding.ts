// useBranding — public-read branding info (brand text + whether
// custom logos are uploaded). Shared across AdminLayout, UserLayout,
// and the login page; the endpoint is unauthenticated so pre-login
// rendering works.
import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";

import { apiClient } from "../apiClient";

type BrandingInfo = {
  panel_brand_text: string;
  has_logo_light: boolean;
  has_logo_dark: boolean;
};

const BRANDING_KEY = ["branding", "public"] as const;

export function useBranding() {
  const query = useQuery<BrandingInfo>({
    queryKey: BRANDING_KEY,
    queryFn: async () => {
      const { data } = await apiClient.get<BrandingInfo>("/branding");
      return data;
    },
    staleTime: 60_000,
    retry: 1,
  });

  const brandText = query.data?.panel_brand_text ?? "";
  const hasLogoLight = query.data?.has_logo_light ?? false;
  const hasLogoDark = query.data?.has_logo_dark ?? false;

  return {
    brandText,
    hasLogoLight,
    hasLogoDark,
    isLoading: query.isLoading,
  };
}

// logoURL returns the public endpoint URL for a given variant, or the
// built-in SVG path when the operator hasn't uploaded one. A cache-
// bust query string based on last update would be ideal but the
// endpoint already sends Cache-Control: max-age=300 and
// re-uploading replaces the on-disk file — browsers re-fetch on the
// next staleTime window.
export function logoURL(variant: "light" | "dark", hasCustom: boolean): string {
  if (hasCustom) {
    return `/api/v1/branding/logo/${variant}`;
  }
  return variant === "dark"
    ? "/images/jabali_logo_dark.svg"
    : "/images/jabali_logo.svg";
}

// useApplyBrandingToTitle keeps document.title in sync with brandText.
// Empty value falls back to "Jabali Panel".
export function useApplyBrandingToTitle() {
  const { brandText } = useBranding();
  useEffect(() => {
    document.title = brandText ? `${brandText} — Panel` : "Jabali Panel";
  }, [brandText]);
}
