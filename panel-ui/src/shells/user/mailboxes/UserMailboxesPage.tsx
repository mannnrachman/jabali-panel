// UserMailboxesPage — tenant-scoped mailbox management.
//
// Lets a user create and manage mailboxes on any domain they own,
// without needing admin access. Email is auto-enabled on domain
// creation (see feat/email-on-by-default) so we no longer surface the
// "enable email + publish DNS records" card here — operators who need
// to see the DNS checklist or disable email entirely manage that from
// the admin Domains → Edit → Email tab. API endpoints are claim-aware
// (panel-API enforces "owner or admin" on every read and write).
import { useEffect, useMemo, useState } from "react";
import { Card, Empty, Select, Skeleton, Space, Typography } from "antd";
import { MailOutlined } from "@ant-design/icons";

import { useListQuery } from "../../../hooks/useQueries";
import { DomainMailboxesSection } from "../../admin/domains/DomainMailboxesSection";
import type { Domain } from "../domains/UserDomainList";

export const UserMailboxesPage = () => {
  const { items: domains, isLoading } = useListQuery<Domain>({
    resource: "domains",
    params: { page: 1, pageSize: 200, sort: "name", order: "asc" },
  });

  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);

  // Auto-select the first email-enabled domain (or the first domain
  // at all if none have email yet) once the list loads — saves a click
  // in the common case where a user has exactly one domain.
  useEffect(() => {
    if (selectedId || domains.length === 0) return;
    const firstEnabled = domains.find((d) => d.email_enabled);
    setSelectedId((firstEnabled ?? domains[0]).id);
  }, [domains, selectedId]);

  const selected = useMemo(
    () => domains.find((d) => d.id === selectedId),
    [domains, selectedId],
  );

  if (isLoading && domains.length === 0) {
    return <Skeleton active paragraph={{ rows: 4 }} />;
  }

  if (domains.length === 0) {
    return (
      <Card>
        <Empty
          image={<MailOutlined style={{ fontSize: 48, color: "#bbb" }} />}
          description={
            <>
              <Typography.Title level={5} style={{ marginBottom: 4 }}>
                No domains yet
              </Typography.Title>
              <Typography.Text type="secondary">
                Add a domain before setting up mail.
              </Typography.Text>
            </>
          }
        />
      </Card>
    );
  }

  return (
    <Space orientation="vertical" size="large" style={{ width: "100%" }}>
      <Card>
        <Space orientation="vertical" style={{ width: "100%" }} size="middle">
          <Typography.Title level={3} style={{ margin: 0 }}>
            <MailOutlined /> Mailboxes
          </Typography.Title>
          {domains.length > 1 && (
            <Select
              value={selectedId}
              onChange={setSelectedId}
              style={{ minWidth: 320 }}
              options={domains.map((d) => ({
                value: d.id,
                label: d.email_enabled ? `${d.name} (email on)` : d.name,
              }))}
            />
          )}
        </Space>
      </Card>

      {selected && (
        <Card title={`Mailboxes — ${selected.name}`}>
          <DomainMailboxesSection
            domainId={selected.id}
            domainOptions={domains
              .filter((d) => d.email_enabled)
              .map((d) => ({ id: d.id, name: d.name }))}
            onDomainCreated={(id) => setSelectedId(id)}
          />
        </Card>
      )}
    </Space>
  );
};
