// AdminIPEdit — admin form for updating a managed IP. Address is
// read-only (delete + re-add to change). Promoting is_default demotes
// the prior default in the same family on the server.
import { useEffect } from "react";
import {
  Button,
  Card,
  Form,
  Input,
  Spin,
  Switch,
  Typography,
  message,
} from "antd";
import { CheckOutlined, CloseOutlined } from "@ant-design/icons";
import { useNavigate, useParams } from "react-router";

import { useOneQuery, useUpdateMutation } from "../../../hooks/useQueries";

type IPEditInput = {
  label: string;
  is_user_selectable: boolean;
  is_default: boolean;
};

type ManagedIP = IPEditInput & {
  id: number;
  address: string;
  family: "ipv4" | "ipv6";
};

export const AdminIPEdit = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [form] = Form.useForm<IPEditInput>();

  const { data, isLoading } = useOneQuery<ManagedIP>({
    resource: "admin/ips",
    id,
  });
  const updateMutation = useUpdateMutation<ManagedIP, IPEditInput>({
    resource: "admin/ips",
  });

  useEffect(() => {
    if (data) {
      form.setFieldsValue({
        label: data.label,
        is_user_selectable: data.is_user_selectable,
        is_default: data.is_default,
      });
    }
  }, [data, form]);

  const handleFinish = async (values: IPEditInput) => {
    if (!id) return;
    try {
      await updateMutation.mutateAsync({ id, input: values });
      message.success("IP updated");
      navigate("/jabali-admin/ips");
    } catch (err: unknown) {
      message.error(err instanceof Error ? err.message : "Failed to update IP");
    }
  };

  if (isLoading && !data) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minHeight: 240,
        }}
      >
        <Spin />
      </div>
    );
  }

  return (
    <Card>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Edit IP — <code>{data?.address}</code>
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        Family: <strong>{data?.family}</strong>. To change the address, delete this
        entry and add a new one.
      </Typography.Paragraph>

      <Form<IPEditInput> form={form} layout="vertical" onFinish={handleFinish}>
        <Form.Item label="Address">
          <Input value={data?.address} readOnly disabled />
        </Form.Item>

        <Form.Item label="Label" name="label">
          <Input placeholder="e.g., 'extra-customer-set'" />
        </Form.Item>

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
          <Form.Item name="is_user_selectable" valuePropName="checked" noStyle>
            <Switch
              checkedChildren={<CheckOutlined />}
              unCheckedChildren={<CloseOutlined />}
            />
          </Form.Item>
          <Typography.Text>User-selectable in domain picker</Typography.Text>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 24 }}>
          <Form.Item name="is_default" valuePropName="checked" noStyle>
            <Switch
              checkedChildren={<CheckOutlined />}
              unCheckedChildren={<CloseOutlined />}
            />
          </Form.Item>
          <Typography.Text>
            Default {data?.family} (used by domains without an explicit binding)
          </Typography.Text>
        </div>

        <Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            loading={updateMutation.isPending}
          >
            Save
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
};
