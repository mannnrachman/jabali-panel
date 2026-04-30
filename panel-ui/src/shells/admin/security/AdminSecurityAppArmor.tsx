// AdminSecurityAppArmor — admin Security tab "AppArmor" sub-tab (M40,
// ADR-0086). Read-only profile list + per-profile complain/enforce
// flip behind a confirm modal.
import { Alert, Badge, Button, Card, Modal, Table, Tag, Typography, message } from "antd";
import { useState } from "react";

import {
  type AppArmorProfile,
  useAppArmorStatus,
  useSetAppArmorMode,
} from "../../../hooks/useSecurityAppArmor";

const MODE_TINT: Record<AppArmorProfile["mode"], "success" | "warning"> = {
  enforce: "success",
  complain: "warning",
};

export const AdminSecurityAppArmor = () => {
  const { data, isLoading, refetch } = useAppArmorStatus();
  const setMode = useSetAppArmorMode();
  const [pendingFlip, setPendingFlip] = useState<{ profile: string; nextMode: AppArmorProfile["mode"] } | null>(null);

  if (isLoading) {
    return (
      <Card title="AppArmor" size="small">
        <Typography.Text type="secondary">Loading…</Typography.Text>
      </Card>
    );
  }

  if (!data?.enabled) {
    return (
      <Card title="AppArmor" size="small">
        <Alert
          type="warning"
          showIcon
          message="AppArmor disabled"
          description={data?.reason || "AppArmor is not active on this host. Reboot may be required if /etc/jabali/.apparmor-grub-pending exists."}
        />
      </Card>
    );
  }

  return (
    <Card
      title="AppArmor"
      size="small"
      extra={
        <Button size="small" onClick={() => refetch()}>
          Refresh
        </Button>
      }
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        Path-based MAC profiles for jabali daemons + critical system services.
        New profiles ship in <Tag color="warning">complain</Tag> mode for a
        7-day burn-in soak; flip to <Tag color="success">enforce</Tag> per
        profile after the soak (or via{" "}
        <code>jabali apparmor flip-mature</code>).
      </Typography.Paragraph>
      <Table
        rowKey="name"
        dataSource={data.profiles}
        loading={isLoading}
        tableLayout="fixed"
        scroll={{ x: "max-content" }}
        size="small"
        pagination={false}
        columns={[
          { title: "Profile", dataIndex: "name", render: (v: string) => <code>{v}</code> },
          {
            title: "Mode",
            dataIndex: "mode",
            width: 140,
            render: (mode: AppArmorProfile["mode"]) => (
              <Badge status={MODE_TINT[mode]} text={mode} />
            ),
          },
          {
            title: "Action",
            width: 200,
            render: (_: unknown, row: AppArmorProfile) => (
              <Button
                size="small"
                type={row.mode === "complain" ? "primary" : "default"}
                onClick={() =>
                  setPendingFlip({
                    profile: row.name,
                    nextMode: row.mode === "complain" ? "enforce" : "complain",
                  })
                }
              >
                Flip to {row.mode === "complain" ? "enforce" : "complain"}
              </Button>
            ),
          },
        ]}
      />

      <Modal
        open={pendingFlip !== null}
        title={
          pendingFlip
            ? `Flip ${pendingFlip.profile} → ${pendingFlip.nextMode}`
            : ""
        }
        okText="Flip"
        onCancel={() => setPendingFlip(null)}
        onOk={() => {
          if (!pendingFlip) return;
          setMode.mutate(
            { profile: pendingFlip.profile, mode: pendingFlip.nextMode },
            {
              onSuccess: () => {
                message.success(`${pendingFlip.profile} → ${pendingFlip.nextMode}`);
                setPendingFlip(null);
              },
              onError: () => message.error("Flip failed — check agent logs"),
            },
          );
        }}
      >
        {pendingFlip?.nextMode === "enforce" ? (
          <Alert
            type="warning"
            showIcon
            message="Enforce will start denying paths/caps not in the profile."
            description="If the profile is missing a path the daemon needs, the daemon will fail. Review complain-mode AVC denials in journalctl -k before flipping."
            style={{ marginBottom: 12 }}
          />
        ) : (
          <Typography.Paragraph>
            Complain mode logs would-deny events without enforcing.
            Useful for tuning the profile after a daemon update changed
            its file/cap requirements.
          </Typography.Paragraph>
        )}
      </Modal>
    </Card>
  );
};
