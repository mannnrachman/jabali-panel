// UpdatesCard — surfaces pending updates with deep links to the
// /jabali-admin/updates page where the operator actually applies them.
// Reads fresh data from the existing M29 endpoints rather than the
// server-status envelope so this card refreshes independently when the
// operator clicks "Check for updates" elsewhere.
import { Card, Space, Tag, Typography } from "antd";
import { Link } from "react-router";

import { useAptCheck, useJabaliCheck } from "../../../hooks/useSystemUpdates";

export function UpdatesCard() {
  const jabali = useJabaliCheck();
  const apt = useAptCheck();
  const jabaliBehind = jabali.data?.behind_count ?? 0;
  const aptCount = apt.data?.total ?? 0;

  return (
    <Card title="Updates" size="small">
      <Space direction="vertical" size={8} style={{ width: "100%" }}>
        <Space>
          <Typography.Text>Jabali panel:</Typography.Text>
          {jabali.data ? (
            jabaliBehind === 0 ? (
              <Tag color="green">up to date</Tag>
            ) : (
              <Link to="/jabali-admin/updates">
                <Tag color="orange">{jabaliBehind} commits behind →</Tag>
              </Link>
            )
          ) : (
            <Typography.Text type="secondary">not checked</Typography.Text>
          )}
        </Space>
        <Space>
          <Typography.Text>System packages:</Typography.Text>
          {apt.data ? (
            aptCount === 0 ? (
              <Tag color="green">up to date</Tag>
            ) : (
              <Link to="/jabali-admin/updates">
                <Tag color="orange">{aptCount} upgradable →</Tag>
              </Link>
            )
          ) : (
            <Typography.Text type="secondary">not checked</Typography.Text>
          )}
        </Space>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0, fontSize: 12 }}>
          Apply updates from the Updates page.
        </Typography.Paragraph>
      </Space>
    </Card>
  );
}
