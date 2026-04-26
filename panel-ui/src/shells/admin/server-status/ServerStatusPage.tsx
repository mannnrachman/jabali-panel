// ServerStatusPage — admin landing for live host health (M31, ADR-0065).
//
// One TanStack Query hits /admin/server-status every 5s while the tab
// is foreground. All sub-sections share the same envelope so the page
// renders cohesively even when one slice is null (timeout / agent
// hiccup) — empty cells show "—" rather than ghost numbers.
//
// Layout: AntD Masonry flows every card into a balanced grid so cards
// of different heights pack without forcing matched rows. Order in
// source = visual order (top-left → top-right → next column).
import { Masonry, Typography } from "antd";

import { useServerStatus } from "../../../hooks/useServerStatus";
import { AlertsBanner } from "./AlertsBanner";
import { DisksTable } from "./DisksTable";
import { CPUMeterCard, LoadMeterCard, MemoryMeterCard, SwapMeterCard } from "./MetersGrid";
import { NetworkTable } from "./NetworkTable";
import { ProcessesCard } from "./ProcessesCard";
import { QueuesCard } from "./QueuesCard";
import { ServicesSummaryCard } from "./ServicesSummaryCard";
import { SystemInfoCard } from "./SystemInfoCard";
import { UpdatesCard } from "./UpdatesCard";
import { UserSlicesCard } from "./UserSlicesCard";

export const ServerStatusPage = () => {
  const q = useServerStatus();
  const env = q.data;

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        Server Status
      </Typography.Title>

      <AlertsBanner alerts={env?.alerts ?? []} />

      <Masonry columns={{ xs: 1, sm: 1, md: 2, lg: 3 }} gutter={16}>
        <ServicesSummaryCard services={env?.services?.services ?? []} />
        <SystemInfoCard
          host={env?.host ?? null}
          network={env?.network ?? null}
          asOf={env?.as_of ?? ""}
          onRefresh={() => q.refetch()}
          isFetching={q.isFetching}
        />
        <CPUMeterCard host={env?.host ?? null} cpu={env?.cpu ?? null} />
        <MemoryMeterCard host={env?.host ?? null} cpu={env?.cpu ?? null} />
        <SwapMeterCard host={env?.host ?? null} cpu={env?.cpu ?? null} />
        <LoadMeterCard host={env?.host ?? null} cpu={env?.cpu ?? null} />
        <DisksTable partitions={env?.host?.partitions ?? []} />
        <NetworkTable interfaces={env?.network?.interfaces ?? []} />
        <UserSlicesCard data={env?.user_slices ?? null} />
        <ProcessesCard processes={env?.processes ?? null} />
        <QueuesCard />
        <UpdatesCard />
      </Masonry>
    </div>
  );
};
