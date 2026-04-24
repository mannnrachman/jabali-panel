// MailTabsPage — unified mail management with tabs for mailboxes, forwarders, etc.
// M6.5 Step 1: tab shell with placeholders.
// Tab implementations filled in by parallel Waves B/C/D.
import { Button, Space, Tabs, type TabsProps } from "antd";
import { PlusOutlined } from "@icons";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { MailboxesTab } from "./tabs/MailboxesTab";
import { ForwardersTab } from "./tabs/ForwardersTab";
import { AutorespondersTab } from "./tabs/AutorespondersTab";
import { CatchAllTab } from "./tabs/CatchAllTab";
import { DisclaimerTab } from "./tabs/DisclaimerTab";
import { SharedFoldersTab } from "./tabs/SharedFoldersTab";
import { LogsTab } from "./tabs/LogsTab";
import { CreateMailboxWizardModal } from "./CreateMailboxWizardModal";

const TAB_KEYS = ["mailboxes", "forwarders", "autoresponders", "catchall", "disclaimer", "shared", "logs"] as const;
type TabKey = (typeof TAB_KEYS)[number];
const DEFAULT_TAB: TabKey = "mailboxes";

export const MailTabsPage = () => {
  const [showCreateMailbox, setShowCreateMailbox] = useState(false);
  const { tab } = useParams<{ tab?: string }>();
  const navigate = useNavigate();
  const activeKey: TabKey = (TAB_KEYS as readonly string[]).includes(tab ?? "") ? (tab as TabKey) : DEFAULT_TAB;

  const tabs: TabsProps["items"] = [
    {
      key: "mailboxes",
      label: "Mailboxes",
      children: <MailboxesTab />,
    },
    {
      key: "forwarders",
      label: "Forwarders",
      children: <ForwardersTab />,
    },
    {
      key: "autoresponders",
      label: "Autoresponders",
      children: <AutorespondersTab />,
    },
    {
      key: "catchall",
      label: "Catch-All",
      children: <CatchAllTab />,
    },
    {
      key: "disclaimer",
      label: "Disclaimer",
      children: <DisclaimerTab />,
    },
    {
      key: "shared",
      label: "Shared Folders",
      children: <SharedFoldersTab />,
    },
    {
      key: "logs",
      label: "Logs",
      children: <LogsTab />,
    },
  ];

  return (
    <div style={{ padding: "20px" }}>
      <div style={{ marginBottom: "16px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <h2 style={{ margin: 0 }}>Mail</h2>
        <Space>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => setShowCreateMailbox(true)}
          >
            New Mailbox
          </Button>
        </Space>
      </div>

      <Tabs
        items={tabs}
        activeKey={activeKey}
        onChange={(key) => navigate(`/jabali-panel/mail/${key}`)}
      />

      {showCreateMailbox && (
        <CreateMailboxWizardModal
          open={showCreateMailbox}
          domains={[]}
          onCancel={() => setShowCreateMailbox(false)}
          onCreated={() => setShowCreateMailbox(false)}
        />
      )}
    </div>
  );
};
