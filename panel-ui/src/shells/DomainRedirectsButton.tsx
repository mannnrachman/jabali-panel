// Redirects modal for per-domain and per-page redirects.
// Opens a modal with two sections:
// 1. Whole-domain redirect toggle + URL + type selector
// 2. Page-level redirects list with add/delete controls
import { useState } from "react";
import {
  SwapOutlined,
  CheckOutlined,
  DeleteOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import {
  Button,
  Modal,
  Switch,
  Row,
  Col,
  Input,
  Select,
  Card,
  Typography,
} from "antd";
import { useInvalidate, useNotification } from "@refinedev/core";

import { apiClient } from "../apiClient";

export type PageRedirect = {
  source: string;
  destination: string;
  type: "301" | "302" | "307" | "308";
};

export type DomainRedirectsTarget = {
  id: string;
  name: string;
  redirect_all_to?: string | null;
  redirect_all_type?: string | null;
  page_redirects?: PageRedirect[] | null;
};

const REDIRECT_TYPE_OPTIONS = [
  { value: "301", label: "Permanent (301) - SEO friendly" },
  { value: "302", label: "Temporary (302)" },
  { value: "307", label: "Temporary (307) - preserves method" },
  { value: "308", label: "Permanent (308) - preserves method" },
];

export const DomainRedirectsButton = ({
  domain,
}: {
  domain: DomainRedirectsTarget;
}) => {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [wholeToggle, setWholeToggle] = useState(!!domain.redirect_all_to);
  const [wholeUrl, setWholeUrl] = useState(domain.redirect_all_to ?? "");
  const [wholeType, setWholeType] = useState(domain.redirect_all_type ?? "301");
  const [pageRedirects, setPageRedirects] = useState<PageRedirect[]>(
    domain.page_redirects ?? []
  );
  const [isSaving, setIsSaving] = useState(false);
  const invalidate = useInvalidate();
  const { open } = useNotification();

  const handleOpenModal = () => {
    // Re-sync from prop in case the value was updated elsewhere
    setWholeToggle(!!domain.redirect_all_to);
    setWholeUrl(domain.redirect_all_to ?? "");
    setWholeType(domain.redirect_all_type ?? "301");
    setPageRedirects(domain.page_redirects ?? []);
    setIsModalOpen(true);
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
  };

  const addPageRedirect = () => {
    setPageRedirects([
      ...pageRedirects,
      { source: "", destination: "", type: "301" },
    ]);
  };

  const removePageRedirect = (idx: number) => {
    setPageRedirects(pageRedirects.filter((_, i) => i !== idx));
  };

  const updatePageRedirect = (
    idx: number,
    key: keyof PageRedirect,
    value: string
  ) => {
    const updated = [...pageRedirects];
    updated[idx] = { ...updated[idx], [key]: value };
    setPageRedirects(updated);
  };

  const handleSave = async () => {
    setIsSaving(true);
    try {
      const body: Record<string, unknown> = {};

      if (wholeToggle) {
        const url = wholeUrl.trim();
        if (!url) {
          open?.({
            type: "error",
            message: "Enter a redirect URL",
          });
          setIsSaving(false);
          return;
        }
        body.redirect_all_to = url;
        body.redirect_all_type = wholeType;
      } else {
        body.redirect_all_to = null;
        body.redirect_all_type = null;
      }

      const clean = pageRedirects.map((pr) => ({
        source: pr.source.trim(),
        destination: pr.destination.trim(),
        type: pr.type,
      }));
      // Don't send half-filled rows — drop blank ones silently
      body.page_redirects = clean.filter((pr) => pr.source && pr.destination);

      await apiClient.patch(`/domains/${domain.id}`, body);
      open?.({
        type: "success",
        message: "Redirects saved",
      });
      invalidate({ resource: "domains", invalidates: ["list"] });
      setIsModalOpen(false);
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      open?.({
        type: "error",
        message: "Failed to save",
        description: e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <>
      <Button
        type="text"
        size="small"
        icon={<SwapOutlined />}
        onClick={handleOpenModal}
      >
        Redirects
      </Button>

      <Modal
        title={`Redirects for ${domain.name}`}
        open={isModalOpen}
        onCancel={handleCloseModal}
        width={720}
        footer={[
          <Button key="cancel" onClick={handleCloseModal}>
            Cancel
          </Button>,
          <Button
            key="save"
            type="primary"
            icon={<CheckOutlined />}
            onClick={handleSave}
            loading={isSaving}
          >
            Save Redirects
          </Button>,
        ]}
      >
        <Typography.Paragraph style={{ color: "#666", marginBottom: 24 }}>
          Redirect this domain to another domain or set up page redirects.
        </Typography.Paragraph>

        {/* Section 1: Redirect Entire Domain */}
        <div style={{ marginBottom: 32 }}>
          <div style={{ marginBottom: 12 }}>
            <Switch
              checked={wholeToggle}
              onChange={setWholeToggle}
              style={{ marginRight: 8 }}
            />
            <Typography.Text strong>Redirect Entire Domain</Typography.Text>
          </div>
          <Typography.Text type="secondary" style={{ marginBottom: 12, display: "block" }}>
            Redirect all traffic from this domain to another domain
          </Typography.Text>

          {wholeToggle && (
            <>
              <Row gutter={16} style={{ marginBottom: 12 }}>
                <Col span={12}>
                  <div style={{ marginBottom: 8 }}>
                    <Typography.Text>
                      Redirect To <span style={{ color: "red" }}>*</span>
                    </Typography.Text>
                  </div>
                  <Input
                    placeholder="https://newdomain.com"
                    value={wholeUrl}
                    onChange={(e) => setWholeUrl(e.target.value)}
                  />
                </Col>
                <Col span={12}>
                  <div style={{ marginBottom: 8 }}>
                    <Typography.Text>
                      Redirect Type <span style={{ color: "red" }}>*</span>
                    </Typography.Text>
                  </div>
                  <Select
                    value={wholeType}
                    onChange={setWholeType}
                    options={REDIRECT_TYPE_OPTIONS}
                    style={{ width: "100%" }}
                  />
                </Col>
              </Row>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                All requests to this domain will be redirected to this URL
              </Typography.Text>
            </>
          )}
        </div>

        {/* Section 2: Page Redirects */}
        <div
          style={{
            opacity: wholeToggle ? 0.5 : 1,
            pointerEvents: wholeToggle ? "none" : "auto",
          }}
        >
          <div style={{ marginBottom: 12 }}>
            <Typography.Text strong>Page Redirects</Typography.Text>
          </div>

          <div style={{ marginBottom: 16 }}>
            {pageRedirects.map((pr, idx) => (
              <Card
                key={idx}
                size="small"
                style={{ marginBottom: 12 }}
                bodyStyle={{ padding: 12 }}
              >
                <div
                  style={{
                    display: "flex",
                    justifyContent: "space-between",
                    alignItems: "center",
                    marginBottom: 12,
                  }}
                >
                  <Typography.Text type="secondary">
                    → {pr.type}
                  </Typography.Text>
                  <Button
                    danger
                    icon={<DeleteOutlined />}
                    type="text"
                    size="small"
                    onClick={() => removePageRedirect(idx)}
                  />
                </div>

                <Row gutter={16} style={{ marginBottom: 12 }}>
                  <Col span={12}>
                    <div style={{ marginBottom: 8 }}>
                      <Typography.Text>
                        Source Path <span style={{ color: "red" }}>*</span>
                      </Typography.Text>
                    </div>
                    <Input
                      placeholder="/old-page"
                      value={pr.source}
                      onChange={(e) =>
                        updatePageRedirect(idx, "source", e.target.value)
                      }
                    />
                    <Typography.Text type="secondary" style={{ fontSize: 12, display: "block", marginTop: 4 }}>
                      Path to redirect from (e.g., /old-page)
                    </Typography.Text>
                  </Col>
                  <Col span={12}>
                    <div style={{ marginBottom: 8 }}>
                      <Typography.Text>
                        Destination URL <span style={{ color: "red" }}>*</span>
                      </Typography.Text>
                    </div>
                    <Input
                      placeholder="https://example.com/new-page"
                      value={pr.destination}
                      onChange={(e) =>
                        updatePageRedirect(idx, "destination", e.target.value)
                      }
                    />
                    <Typography.Text type="secondary" style={{ fontSize: 12, display: "block", marginTop: 4 }}>
                      Full URL to redirect to
                    </Typography.Text>
                  </Col>
                </Row>

                <div style={{ marginBottom: 0 }}>
                  <div style={{ marginBottom: 8 }}>
                    <Typography.Text>
                      Type <span style={{ color: "red" }}>*</span>
                    </Typography.Text>
                  </div>
                  <Select
                    value={pr.type}
                    onChange={(value) =>
                      updatePageRedirect(idx, "type", value)
                    }
                    options={REDIRECT_TYPE_OPTIONS}
                    style={{ width: "100%" }}
                  />
                </div>
              </Card>
            ))}
          </div>

          <div style={{ textAlign: "center", marginBottom: 16 }}>
            <Button icon={<PlusOutlined />} onClick={addPageRedirect}>
              Add Page Redirect
            </Button>
          </div>

          <Typography.Text type="secondary">
            Redirect specific paths to other URLs
          </Typography.Text>
        </div>
      </Modal>
    </>
  );
};
