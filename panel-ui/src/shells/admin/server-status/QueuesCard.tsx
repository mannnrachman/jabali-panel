// QueuesCard — placeholder for v1. Aggregator does not yet ship queue
// stats; once the backend extension lands (system.queues fan-out into
// MariaDB SHOW STATUS, nginx stub, stalwart-cli queue), each row swaps
// from "—" to a live value. Built now so the page layout is final and
// step 5's UI scope is satisfied.
import { Card, Space, Statistic, Tooltip, Typography } from "antd";

export function QueuesCard() {
  return (
    <Card title="Queues" size="small">
      <Space size={24} wrap>
        <Tooltip title="MariaDB connections (current / max)">
          <Statistic title="MariaDB conns" value="—" />
        </Tooltip>
        <Tooltip title="Nginx active connections">
          <Statistic title="Nginx active" value="—" />
        </Tooltip>
        <Tooltip title="Stalwart mail queue size">
          <Statistic title="Mail queue" value="—" />
        </Tooltip>
      </Space>
      <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0, fontSize: 12 }}>
        Queue stats land in a follow-up; this card reserves layout space.
      </Typography.Paragraph>
    </Card>
  );
}
