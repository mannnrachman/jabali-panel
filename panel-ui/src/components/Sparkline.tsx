// Sparkline — minimal inline-SVG line chart for per-day bandwidth or
// any equivalent time series. No dependency on a chart library; the
// panel imports zero third-party charting (recharts adds ~200KB
// gzipped, not worth it for one card).
//
// Caller passes pre-aggregated points; component computes the
// y-extent and emits an SVG <path> with proportional <line> for
// each tick.
import { theme as antdTheme } from "antd";

export interface SparklinePoint {
  /** ISO date or x-axis label. */
  x: string;
  /** Numeric value (bytes, requests, etc.). */
  y: number;
}

export interface SparklineProps {
  data: SparklinePoint[];
  /** Pixel width of the SVG. Defaults to 200. */
  width?: number;
  /** Pixel height of the SVG. Defaults to 40. */
  height?: number;
  /** Stroke colour for the line. Defaults to AntD primary. */
  color?: string;
  /** When true, fills under the line with a translucent variant. */
  filled?: boolean;
  /** Hover-tooltip rendering hook for the y-value. */
  formatY?: (n: number) => string;
}

export function Sparkline({
  data,
  width = 200,
  height = 40,
  color,
  filled = true,
  formatY,
}: SparklineProps) {
  const { token } = antdTheme.useToken();
  const stroke = color ?? token.colorPrimary;

  if (data.length === 0) {
    return (
      <svg width={width} height={height}>
        <rect width={width} height={height} fill={token.colorFillTertiary} rx={2} />
      </svg>
    );
  }

  const values = data.map((p) => p.y);
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const span = max - min || 1;
  const stepX = data.length > 1 ? width / (data.length - 1) : 0;
  const points = data.map((p, i) => {
    const x = i * stepX;
    const y = height - ((p.y - min) / span) * height;
    return { x, y };
  });

  const path = points
    .map((p, i) => `${i === 0 ? "M" : "L"} ${p.x.toFixed(2)} ${p.y.toFixed(2)}`)
    .join(" ");

  // Closed path for fill: extend to bottom-right + bottom-left.
  const fillPath =
    points.length > 0
      ? `${path} L ${(points[points.length - 1]?.x ?? 0).toFixed(2)} ${height} L 0 ${height} Z`
      : "";

  return (
    <svg
      width={width}
      height={height}
      role="img"
      aria-label={
        formatY
          ? `Sparkline: max ${formatY(max)}, min ${formatY(min)}, ${data.length} points`
          : `Sparkline of ${data.length} points`
      }
    >
      {filled && fillPath && (
        <path d={fillPath} fill={stroke} fillOpacity={0.15} />
      )}
      <path d={path} fill="none" stroke={stroke} strokeWidth={1.5} />
    </svg>
  );
}
