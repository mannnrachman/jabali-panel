// AdminIPDrawer — create + edit drawer for managed IPs (M24).
//
// Replaces /jabali-admin/ips/create and /jabali-admin/ips/edit/:id
// page routes with a Drawer. Per docs/CONVENTIONS.md, create + edit
// share a single Drawer surface; address is read-only on edit (delete
// + re-add to change).
import {
  Alert,
  Button,
  Drawer,
  Form,
  Grid,
  Input,
  Space,
  Spin,
  Switch,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";

import { CheckOutlined, CloseOutlined } from "@icons";

import { useCreateMutation, useOneQuery, useUpdateMutation } from "../../../hooks/useQueries";

type IPCreateInput = {
  address: string;
  label: string;
  is_user_selectable: boolean;
};

type IPEditInput = {
  label: string;
  is_user_selectable: boolean;
  is_default: boolean;
};

type IPCreated = {
  id: number;
  warnings?: string[];
};

type ManagedIP = IPEditInput & {
  id: number;
  address: string;
  family: "ipv4" | "ipv6";
};

type FormValues = IPCreateInput & Partial<Pick<IPEditInput, "is_default">>;

const RESOURCE = "admin/ips";

// IPv4 + IPv6 regex pair. Server-side validation is the source of
// truth; this just catches obvious typos client-side.
const IPV4 = /^(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)$/;
const IPV6 = /^[0-9a-fA-F:]+$/;
function isProbablyIP(addr: string): boolean {
  return IPV4.test(addr) || (addr.includes(":") && IPV6.test(addr));
}

export interface AdminIPDrawerProps {
  open: boolean;
  onClose: () => void;
  /** Existing managed-IP id for edit mode. Undefined = create. */
  editingId?: number;
}

export function AdminIPDrawer({ open, onClose, editingId }: AdminIPDrawerProps) {
  const [form] = Form.useForm<FormValues>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg ?? (typeof window !== "undefined" ? window.innerWidth >= 992 : true);
  const isEdit = Boolean(editingId);
  const [warnings, setWarnings] = useState<string[]>([]);

  const { data: existing, isLoading } = useOneQuery<ManagedIP>({
    resource: RESOURCE,
    id: editingId !== undefined ? String(editingId) : undefined,
    enabled: isEdit && open,
  });

  const create = useCreateMutation<IPCreated, IPCreateInput>({ resource: RESOURCE });
  const update = useUpdateMutation<ManagedIP, IPEditInput>({ resource: RESOURCE });

  useEffect(() => {
    if (!open) return;
    setWarnings([]);
    if (isEdit && existing) {
      form.resetFields();
      form.setFieldsValue({
        address: existing.address,
        label: existing.label,
        is_user_selectable: existing.is_user_selectable,
        is_default: existing.is_default,
      });
    } else if (!isEdit) {
      form.resetFields();
      form.setFieldsValue({ label: "", is_user_selectable: false });
    }
  }, [open, isEdit, existing, form]);

  const handleFinish = async (values: FormValues) => {
    setWarnings([]);
    try {
      if (isEdit && editingId !== undefined) {
        await update.mutateAsync({
          id: String(editingId),
          input: {
            label: values.label,
            is_user_selectable: values.is_user_selectable,
            is_default: values.is_default ?? false,
          },
        });
        message.success("IP updated");
        onClose();
      } else {
        const result = await create.mutateAsync({
          address: values.address.trim(),
          label: values.label,
          is_user_selectable: values.is_user_selectable,
        });
        if (result.warnings && result.warnings.length > 0) {
          setWarnings(result.warnings);
          message.warning("IP added with warnings — review below");
          return;
        }
        message.success("IP added to pool");
        onClose();
      }
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Save failed");
    }
  };

  return (
    <Drawer
      title={isEdit ? `Edit IP — ${existing?.address ?? ""}` : "Add IP address"}
      open={open}
      onClose={onClose}
      width={isDesktop ? 540 : undefined}
      placement="right"
      destroyOnClose
    >
      {isEdit && isLoading && !existing ? (
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
      ) : (
        <>
          {!isEdit && (
            <>
              <Alert
                type="info"
                showIcon
                style={{ marginBottom: 12 }}
                message="Persistence is your responsibility"
                description={
                  <span>
                    jabali binds ephemerally via <code>ip addr add</code>. For the binding to
                    survive a reboot, add the address via your provider&apos;s network
                    configuration (Hetzner robot, Vultr additional IP, netplan, or{" "}
                    <code>/etc/network/interfaces.d/</code>).
                  </span>
                }
              />
              <Alert
                type="warning"
                showIcon
                style={{ marginBottom: 16 }}
                message="Verify your firewall"
                description={
                  <span>
                    After adding, ensure your host firewall allows inbound TCP 80 and 443 to
                    this address.
                  </span>
                }
              />
            </>
          )}

          {isEdit && existing && (
            <Typography.Paragraph type="secondary">
              Family: <strong>{existing.family}</strong>. To change the address, delete this
              entry and add a new one.
            </Typography.Paragraph>
          )}

          <Form<FormValues>
            form={form}
            layout="vertical"
            initialValues={{ label: "", is_user_selectable: false }}
            onFinish={handleFinish}
          >
            <Form.Item
              label="Address"
              name="address"
              rules={
                isEdit
                  ? []
                  : [
                      { required: true, message: "IP address is required" },
                      {
                        validator: (_, v) =>
                          v && !isProbablyIP(String(v).trim())
                            ? Promise.reject(new Error("Must be a valid IPv4 or IPv6 address"))
                            : Promise.resolve(),
                      },
                    ]
              }
            >
              <Input
                placeholder="203.0.113.50 or 2001:db8::1"
                readOnly={isEdit}
                disabled={isEdit}
              />
            </Form.Item>

            <Form.Item
              label="Label"
              name="label"
              tooltip="Optional human-readable note (provider, purpose, etc.)"
            >
              <Input placeholder="e.g., 'extra-customer-set'" />
            </Form.Item>

            <Form.Item
              name="is_user_selectable"
              label="User-selectable in domain picker"
              valuePropName="checked"
            >
              <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
            </Form.Item>

            {isEdit && (
              <Form.Item
                name="is_default"
                label={`Default ${existing?.family ?? "family"} (used by domains without an explicit binding)`}
                valuePropName="checked"
              >
                <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
              </Form.Item>
            )}

            {warnings.length > 0 && (
              <Alert
                type="warning"
                showIcon
                style={{ marginBottom: 16 }}
                message="Post-bind probe warnings"
                description={
                  <ul style={{ marginBottom: 0 }}>
                    {warnings.map((w) => (
                      <li key={w}>{w}</li>
                    ))}
                  </ul>
                }
              />
            )}

            <Form.Item>
              <Space>
                <Button
                  type="primary"
                  htmlType="submit"
                  loading={create.isPending || update.isPending}
                >
                  {isEdit ? "Save" : "Add IP"}
                </Button>
                <Button onClick={onClose}>Cancel</Button>
              </Space>
            </Form.Item>
          </Form>
        </>
      )}
    </Drawer>
  );
}
