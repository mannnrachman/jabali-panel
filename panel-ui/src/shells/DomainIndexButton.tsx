// Index Manager — picks which file(s) nginx serves as the default
// directory index. Values are an enum the agent maps to concrete
// `index ...;` directives; see indexDirectiveFor() in the agent.
import { useEffect, useState } from "react";
import { FileTextOutlined, CheckOutlined } from "@ant-design/icons";
import { Button, Modal, Radio, Typography, notification } from "antd";
import { useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export type IndexPriority =
  | "html_first"
  | "php_first"
  | "html_only"
  | "php_only"
  | "full";

export type DomainIndexTarget = {
  id: string;
  name: string;
  index_priority?: IndexPriority | string | null;
};

// Options in the order they appear in the user-facing radio group.
// The labels mirror the screenshots the user supplied.
const options: { value: IndexPriority; label: string }[] = [
  { value: "php_first", label: "PHP first (index.php, then index.html)" },
  { value: "html_first", label: "HTML first (index.html, then index.php)" },
  { value: "php_only", label: "PHP only (index.php)" },
  { value: "html_only", label: "HTML only (index.html)" },
  { value: "full", label: "PHP, HTML, HTM (full support)" },
];

export const DomainIndexButton = ({ domain }: { domain: DomainIndexTarget }) => {
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [value, setValue] = useState<IndexPriority>(
    (domain.index_priority as IndexPriority) || "html_first",
  );
  const qc = useQueryClient();

  // Re-sync from prop each time the modal opens so another user's
  // concurrent edit doesn't get silently clobbered.
  useEffect(() => {
    if (open) {
      setValue((domain.index_priority as IndexPriority) || "html_first");
    }
  }, [open, domain.index_priority]);

  const handleSave = async () => {
    setSaving(true);
    try {
      await apiClient.patch(`/domains/${domain.id}`, {
        index_priority: value,
      });
      notification.success({ message: "Index priority saved" });
      qc.invalidateQueries({ queryKey: ["list", "domains"] });
      qc.invalidateQueries({ queryKey: ["one", "domains", domain.id] });
      setOpen(false);
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      notification.error({
        message: "Failed to save",
        description: e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Button
        type="link"
        icon={<FileTextOutlined />}
        onClick={() => setOpen(true)}
      >
        Index
      </Button>
      <Modal
        title={`Index Manager for ${domain.name}`}
        open={open}
        onCancel={() => setOpen(false)}
        width={560}
        footer={[
          <Button key="cancel" onClick={() => setOpen(false)}>
            Cancel
          </Button>,
          <Button
            key="save"
            type="primary"
            icon={<CheckOutlined />}
            loading={saving}
            onClick={handleSave}
          >
            Submit
          </Button>,
        ]}
      >
        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          Set the default directory index files
        </Typography.Paragraph>
        <Typography.Text strong>
          Directory Index Priority <span style={{ color: "#ff4d4f" }}>*</span>
        </Typography.Text>
        <Radio.Group
          value={value}
          onChange={(e) => setValue(e.target.value)}
          style={{ display: "block", marginTop: 12 }}
        >
          {options.map((opt) => (
            <Radio
              key={opt.value}
              value={opt.value}
              style={{ display: "block", padding: "8px 0" }}
            >
              {opt.label}
            </Radio>
          ))}
        </Radio.Group>
        <Typography.Text
          type="secondary"
          style={{ display: "block", marginTop: 12, fontSize: 12 }}
        >
          Choose which file should be served as the default index
        </Typography.Text>
      </Modal>
    </>
  );
};
