import { Edit, useForm } from "@refinedev/antd";
import { Form, Input, InputNumber, Select, Table, Button, Modal, Space, Tag } from "antd";
import { useState } from "react";
import { useList } from "@refinedev/core";

type PHPPoolInput = {
  pm_mode: string;
  pm_max_children: number;
  process_idle_timeout_seconds: number;
};

type IniOverride = {
  id: string;
  directive: string;
  value: string;
  kind: "value" | "flag";
};

type IniOverrideInput = {
  directive: string;
  value: string;
  kind: "value" | "flag";
};

export const PHPPoolEdit = () => {
  const { formProps, saveButtonProps, id } = useForm<PHPPoolInput>({
    resource: "admin-php-pools",
    action: "edit",
  });

  const [isOverrideModalOpen, setIsOverrideModalOpen] = useState(false);
  const [overrideForm, setOverrideForm] = useState<IniOverrideInput>({ directive: "", value: "", kind: "value" });

  const { data: overridesData, refetch: refetchOverrides, isLoading: overridesLoading } = useList<IniOverride>({
    resource: `admin-php-pools/${id}/ini-overrides`,
    queryOptions: {
      enabled: !!id,
    },
  });

  const overrides = overridesData?.data || [];

  const handleAddOverride = async () => {
    if (!overrideForm.directive.trim()) {
      return;
    }
    // POST to create ini override
    try {
      await fetch(`/api/v1/php-pools/${id}/ini-overrides`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(overrideForm),
      });
      setOverrideForm({ directive: "", value: "", kind: "value" });
      setIsOverrideModalOpen(false);
      refetchOverrides();
    } catch (error) {
      console.error("Failed to add override:", error);
    }
  };

  const handleDeleteOverride = async (overrideId: string) => {
    try {
      await fetch(`/api/v1/php-pools/${id}/ini-overrides/${overrideId}`, {
        method: "DELETE",
      });
      refetchOverrides();
    } catch (error) {
      console.error("Failed to delete override:", error);
    }
  };

  return (
    <Edit saveButtonProps={saveButtonProps}>
      <Form {...formProps} layout="vertical">
        <Form.Item
          label="Process Mode"
          name="pm_mode"
          rules={[{ required: true, message: "Process mode is required" }]}
        >
          <Select placeholder="Select process manager mode">
            <Select.Option value="static">static</Select.Option>
            <Select.Option value="dynamic">dynamic</Select.Option>
            <Select.Option value="ondemand">ondemand</Select.Option>
          </Select>
        </Form.Item>

        <Form.Item
          label="Max Children"
          name="pm_max_children"
          rules={[{ required: true, message: "Max children is required" }]}
        >
          <InputNumber min={1} placeholder="Number of child processes" />
        </Form.Item>

        <Form.Item
          label="Process Idle Timeout (seconds)"
          name="process_idle_timeout_seconds"
          rules={[{ required: false }]}
        >
          <InputNumber min={0} placeholder="Idle timeout in seconds" />
        </Form.Item>

        <Form.Item label="PHP Version" name="php_version">
          <Input disabled />
        </Form.Item>
      </Form>

      {/* INI Overrides Section */}
      <div style={{ marginTop: 32, paddingTop: 24, borderTop: "1px solid #f0f0f0" }}>
        <Space style={{ marginBottom: 16, display: "flex", justifyContent: "space-between" }}>
          <h3 style={{ margin: 0 }}>INI Overrides</h3>
          <Button type="primary" onClick={() => setIsOverrideModalOpen(true)}>
            Add Override
          </Button>
        </Space>

        <Table<IniOverride>
          columns={[
            {
              dataIndex: "directive",
              title: "Directive",
              key: "directive",
            },
            {
              dataIndex: "value",
              title: "Value",
              key: "value",
              render: (value: string) => (
                <div style={{ maxWidth: 300, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {value}
                </div>
              ),
            },
            {
              dataIndex: "kind",
              title: "Kind",
              key: "kind",
              render: (kind: string) => <Tag color={kind === "flag" ? "orange" : "blue"}>{kind}</Tag>,
            },
            {
              title: "Actions",
              key: "actions",
              width: 100,
              render: (_, record) => (
                <Button
                  type="text"
                  danger
                  size="small"
                  onClick={() => handleDeleteOverride(record.id)}
                >
                  Delete
                </Button>
              ),
            },
          ]}
          dataSource={overrides}
          loading={overridesLoading}
          rowKey="id"
          pagination={false}
          bordered
        />

        <Modal
          title="Add INI Override"
          open={isOverrideModalOpen}
          onOk={handleAddOverride}
          onCancel={() => {
            setIsOverrideModalOpen(false);
            setOverrideForm({ directive: "", value: "", kind: "value" });
          }}
        >
          <Form layout="vertical">
            <Form.Item label="Directive">
              <Input
                placeholder="e.g., memory_limit"
                value={overrideForm.directive}
                onChange={(e) =>
                  setOverrideForm({ ...overrideForm, directive: e.target.value })
                }
              />
            </Form.Item>
            <Form.Item label="Value">
              <Input
                placeholder="e.g., 256M"
                value={overrideForm.value}
                onChange={(e) =>
                  setOverrideForm({ ...overrideForm, value: e.target.value })
                }
              />
            </Form.Item>
            <Form.Item label="Kind">
              <Select
                value={overrideForm.kind}
                onChange={(value) =>
                  setOverrideForm({ ...overrideForm, kind: value })
                }
              >
                <Select.Option value="value">value</Select.Option>
                <Select.Option value="flag">flag</Select.Option>
              </Select>
            </Form.Item>
          </Form>
        </Modal>
      </div>
    </Edit>
  );
};
