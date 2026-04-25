// ServerStatusPage — admin landing for live host health (M31, ADR-0065).
//
// One TanStack Query hits /admin/server-status every 5s while the tab
// is foreground. All sub-sections share the same envelope so the page
// renders cohesively even when one slice is null (timeout / agent
// hiccup) — empty cells show "—" rather than ghost numbers.
import { Col, Row, Space, Typography } from "antd";

import { useServerStatus } from "../../../hooks/useServerStatus";
import { AlertsBanner } from "./AlertsBanner";
import { DisksTable } from "./DisksTable";
import { HostHeaderCard } from "./HostHeaderCard";
import { MetersGrid } from "./MetersGrid";
import { NetworkTable } from "./NetworkTable";
import { ProcessesCard } from "./ProcessesCard";
import { QueuesCard } from "./QueuesCard";
import { ServicesGrid } from "./ServicesGrid";
import { UpdatesCard } from "./UpdatesCard";

export const ServerStatusPage = () => {
  const q = useServerStatus();
  const env = q.data;

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        Server Status
      </Typography.Title>

      <AlertsBanner alerts={env?.alerts ?? []} />

      <Space direction="vertical" size={16} style={{ width: "100%" }}>
        <HostHeaderCard
          host={env?.host ?? null}
          asOf={env?.as_of ?? ""}
          onRefresh={() => q.refetch()}
          isFetching={q.isFetching}
        />

        <MetersGrid host={env?.host ?? null} cpu={env?.cpu ?? null} />

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={12}>
            <DisksTable partitions={env?.host?.partitions ?? []} />
          </Col>
          <Col xs={24} lg={12}>
            <NetworkTable interfaces={env?.network?.interfaces ?? []} />
          </Col>
        </Row>

        <ServicesGrid services={env?.services?.services ?? []} />

        <Row gutter={[16, 16]}>
          <Col xs={24} md={12} lg={8}>
            <QueuesCard />
          </Col>
          <Col xs={24} md={12} lg={8}>
            <ProcessesCard processes={env?.processes ?? null} />
          </Col>
          <Col xs={24} md={24} lg={8}>
            <UpdatesCard />
          </Col>
        </Row>
      </Space>
    </div>
  );
};
