// AdminSecurityTrust — M43 Step 3 unified policy view.
//
// Read-only single-page surface for the operator's "who decides what
// gets blocked?" question. Composes existing hooks for CrowdSec
// decisions + UFW status; flags any UFW rule with a non-default
// `from` address as a candidate for migration to a CrowdSec
// decision (per ADR-0089 target: CrowdSec is the single IP-trust
// source of truth). Rate-cap and AppSec panels are TODO links to
// existing tabs until M43 Wave B/C ships their dedicated views.
import { Alert, Card, Empty, Space, Statistic, Table, Tag, Typography } from "antd";
import { LinkOutlined } from "@icons";
import { useNavigate } from "react-router";

import { useCrowdsecDecisions } from "../../../hooks/useSecurityCrowdsec";
import { useUfwStatus, type UfwRule } from "../../../hooks/useSecurityUfw";

// A UFW rule is flagged as an "IP rule" when `from` is anything other
// than the wildcard (`Anywhere`/`Anywhere (v6)`). Per ADR-0089 these
// are the rules that should migrate to CrowdSec — UFW reduces to
// port policy only.
const isIpRule = (r: UfwRule) =>
  r.from !== "Anywhere" && r.from !== "Anywhere (v6)" && r.from !== "" ;

export const AdminSecurityTrust = () => {
  const navigate = useNavigate();
  const decisions = useCrowdsecDecisions("ip");
  const ufw = useUfwStatus();

  const csCount = decisions.data?.length ?? 0;
  const ufwIpRules = (ufw.data?.rules ?? []).filter(isIpRule);
  const ufwIpCount = ufwIpRules.length;

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="Trust hierarchy (M43)"
        description={
          <>
            <strong>CrowdSec</strong> is the single risk authority for IPs.
            <strong> firewall-bouncer / nginx-bouncer</strong> are the enforcers.
            <strong> nginx limit_req</strong> is anti-noise (not security).
            <strong> UFW</strong> is the static port baseline — it should hold zero
            <code> from &lt;ip&gt; </code> rules. Any UFW IP rules listed below are
            candidates for migration to CrowdSec.
          </>
        }
      />

      <Card size="small" title="IP verdicts">
        <Space size={32} wrap>
          <Statistic title="CrowdSec active IP decisions" value={csCount} />
          <Statistic
            title="UFW IP rules"
            value={ufwIpCount}
            valueStyle={ufwIpCount > 0 ? { color: "#cf1322" } : undefined}
            suffix={ufwIpCount > 0 ? <Tag color="warning">migrate</Tag> : null}
          />
        </Space>

        {ufwIpCount > 0 ? (
          <>
            <Typography.Paragraph style={{ marginTop: 16, marginBottom: 8 }} type="secondary">
              These UFW rules duplicate CrowdSec's authority. Until M43 Wave B
              ships the migration CLI, an admin can replicate each rule via
              the CrowdSec tab → Decisions → Add, then remove the UFW rule.
            </Typography.Paragraph>
            <Table
              size="small"
              rowKey="num"
              dataSource={ufwIpRules}
              pagination={false}
              columns={[
                { title: "#", dataIndex: "num", width: 50 },
                { title: "Action", dataIndex: "action", width: 100 },
                { title: "From", dataIndex: "from" },
                { title: "Port", dataIndex: "port", width: 100 },
                { title: "Proto", dataIndex: "proto", width: 80 },
              ]}
            />
          </>
        ) : (
          <Empty
            style={{ marginTop: 16 }}
            description="No UFW IP rules — clean baseline."
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          />
        )}
      </Card>

      <Card
        size="small"
        title="Rate caps (anti-noise pre-filter)"
        extra={
          <a onClick={() => navigate("/jabali-admin/security?tab=ufw")}>
            <LinkOutlined /> manage in Firewall tab
          </a>
        }
      >
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          nginx <code>limit_req</code> applies per-domain when{" "}
          <code>rate_limit_rps</code> is set. <strong>Treat as anti-noise damping
          only</strong> — it's blind to identity and attack patterns. Hard caps
          on <code>/jabali-panel/login</code>, <code>/admin-api/*</code>, and{" "}
          <code>/xmlrpc.php</code> are kept by design (M43 Step 5 contract).
        </Typography.Paragraph>
      </Card>

      <Card
        size="small"
        title="AppSec rules"
        extra={
          <a onClick={() => navigate("/jabali-admin/security?tab=crowdsec&sub=appsec")}>
            <LinkOutlined /> manage in CrowdSec tab
          </a>
        }
      >
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          Inline request inspection runs in CrowdSec AppSec engine
          (127.0.0.1:7422). Verdicts feed scenarios; bouncer enforces.
          Single risk authority — no duplicate WAF layer. ADR-0055
          superseded by ADR-0060.
        </Typography.Paragraph>
      </Card>
    </Space>
  );
};
