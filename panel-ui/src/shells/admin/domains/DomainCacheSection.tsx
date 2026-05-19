// DomainCacheSection — per-domain nginx FastCGI micro-cache toggle
// (ADR-0108). Self-contained like DomainEmailSection/DomainIPACLSection:
// loads its own state, flips the switch (DB-as-truth → reconciler
// re-renders the vhost), and exposes a manual purge button.
import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Popconfirm, Skeleton, Space, Switch, message } from "antd";
import { CheckOutlined, CloseOutlined, ThunderboltOutlined } from "@icons";

import { apiClient } from "../../../apiClient";

type CacheState = {
  domain_id: string;
  domain_name: string;
  enabled: boolean;
};

type Props = { domainId: string };

export const DomainCacheSection = ({ domainId }: Props) => {
  const [state, setState] = useState<CacheState | null>(null);
  const [loading, setLoading] = useState(true);
  const [toggling, setToggling] = useState(false);
  const [purging, setPurging] = useState(false);

  const fetchState = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiClient.get<CacheState>(`/domains/${domainId}/cache`);
      setState(res.data);
    } catch {
      message.error("Failed to load cache status");
    } finally {
      setLoading(false);
    }
  }, [domainId]);

  useEffect(() => {
    fetchState();
  }, [fetchState]);

  const onFlip = async (next: boolean) => {
    setToggling(true);
    try {
      const res = await apiClient.put<CacheState>(`/domains/${domainId}/cache`, {
        enabled: next,
      });
      setState(res.data);
      message.success(
        next
          ? "Caching enabled — applying to the site's nginx config"
          : "Caching disabled",
      );
    } catch {
      message.error("Failed to toggle caching");
      await fetchState();
    } finally {
      setToggling(false);
    }
  };

  const onPurge = async () => {
    setPurging(true);
    try {
      await apiClient.post(`/domains/${domainId}/cache/purge`);
      message.success("Cache purged");
    } catch {
      message.error("Failed to purge cache");
    } finally {
      setPurging(false);
    }
  };

  if (loading) return <Skeleton active paragraph={{ rows: 1 }} />;

  const enabled = !!state?.enabled;

  return (
    <Space direction="vertical" style={{ width: "100%" }}>
      <Space size="middle" align="center">
        <Switch
          checkedChildren={<CheckOutlined />}
          unCheckedChildren={<CloseOutlined />}
          checked={enabled}
          loading={toggling}
          onChange={onFlip}
        />
        <span>nginx page cache</span>
        <Popconfirm
          title="Purge cached pages for this domain?"
          okText="Purge"
          onConfirm={onPurge}
          disabled={!enabled}
        >
          <Button
            icon={<ThunderboltOutlined />}
            loading={purging}
            disabled={!enabled}
          >
            Purge cache
          </Button>
        </Popconfirm>
      </Space>
      <Alert
        type="info"
        showIcon
        title="Short-lived page cache (60s)"
        description="Caches PHP/HTML responses for 60 seconds. Automatically bypassed for POST requests, logged-in WordPress users, carts/checkout, wp-admin and session cookies — so dynamic and authenticated pages stay correct. Static assets (css/js/images/fonts) also get long-lived browser cache headers."
      />
    </Space>
  );
};
