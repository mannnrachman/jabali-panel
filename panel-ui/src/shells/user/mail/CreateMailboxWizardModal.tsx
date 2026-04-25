// CreateMailboxWizardModal — 2-step mailbox creation flow.
//
// Step 1: just the Domain select. Submit is disabled until a domain is
//         picked. No other fields visible so first-time users aren't
//         hit with a wall of inputs before the target domain is chosen.
// Step 2: once a domain is selected, the rest of the form materialises
//         (local part, password, quota). Same submit button; now
//         enabled once local_part is filled.
//
// The modal calls POST /domains/:id/mailboxes on submit and surfaces
// the reveal-once password via the onCreated callback — the parent
// page pops the DatabaseUserPasswordModal with it.
import { Alert, Button, Drawer, Form, Grid, Input, InputNumber, Select, Space } from "antd";

import { PasswordInput } from "../../../components/PasswordInput";
import {
  useCreateMailbox,
  type CreateMailboxResponse,
} from "../../../hooks/useMailboxes";

// Quota presets in bytes — 1 GiB default matches the DB column default;
// the 16 MiB floor is panel-api's minMailboxQuotaBytes.
const QUOTA_DEFAULT_BYTES = 1 * 1024 * 1024 * 1024;
const QUOTA_MIN_BYTES = 16 * 1024 * 1024;

function parseQuotaInput(v: number | string | null | undefined): number | undefined {
  if (v === null || v === undefined || v === "") return undefined;
  const n = typeof v === "number" ? v : Number(v);
  if (Number.isNaN(n) || n <= 0) return undefined;
  return Math.floor(n * 1024 * 1024); // UI unit is MiB, wire unit is bytes
}

type DomainOption = { id: string; name: string };

type Props = {
  open: boolean;
  domains: DomainOption[];
  onCancel: () => void;
  onCreated: (resp: CreateMailboxResponse) => void;
};

type FormValues = {
  domain_id?: string;
  local_part?: string;
  password?: string;
  quota_mib?: number;
};

export const CreateMailboxWizardModal = ({
  open,
  domains,
  onCancel,
  onCreated,
}: Props) => {
  const [form] = Form.useForm<FormValues>();
  const createMutation = useCreateMailbox();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;

  // Watching domain_id is what drives step-2 reveal. Form.useWatch
  // returns undefined until the user opens the Select and picks a
  // value, so the cascading fields stay hidden on first render.
  const watchedDomainId = Form.useWatch("domain_id", form);
  const chosenDomain = domains.find((d) => d.id === watchedDomainId);

  const onOk = async () => {
    // validateFields rejects when required rules fail; AntD surfaces
    // the per-field error text inline, so we just swallow the reject
    // here and let the user correct and retry.
    let values: FormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    if (!values.domain_id || !values.local_part) return;
    try {
      const resp = await createMutation.mutateAsync({
        domainId: values.domain_id,
        input: {
          local_part: values.local_part,
          password: values.password || undefined,
          quota_bytes: parseQuotaInput(values.quota_mib),
        },
      });
      form.resetFields();
      onCreated(resp);
    } catch (err) {
      const detail =
        (err as { response?: { data?: { detail?: string; error?: string } } })?.response
          ?.data?.detail ??
        (err as { response?: { data?: { detail?: string; error?: string } } })?.response
          ?.data?.error;
      // Surface validation/conflict errors as a form-level alert rather
      // than a floating toast so the user sees them next to the inputs
      // that caused them.
      form.setFields([
        {
          name: "local_part",
          errors: [detail ?? "Failed to create mailbox"],
        },
      ]);
    }
  };

  const onCancelInternal = () => {
    form.resetFields();
    onCancel();
  };

  return (
    <Drawer
      title="Create mailbox"
      open={open}
      onClose={onCancelInternal}
      width={isDesktop ? 520 : undefined}
      placement="right"
      destroyOnClose
      extra={
        <Space>
          <Button onClick={onCancelInternal}>Cancel</Button>
          <Button type="primary" loading={createMutation.isPending} onClick={onOk}>
            Create mailbox
          </Button>
        </Space>
      }
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{ quota_mib: QUOTA_DEFAULT_BYTES / 1024 / 1024 }}
      >
        <Form.Item
          label="Domain"
          name="domain_id"
          rules={[{ required: true, message: "Pick a domain" }]}
          tooltip="Only domains with email enabled appear here."
        >
          <Select
            showSearch
            optionFilterProp="label"
            placeholder="Select a domain"
            options={domains.map((d) => ({ value: d.id, label: d.name }))}
          />
        </Form.Item>

        {chosenDomain && (
          <>
            <Form.Item
              label="Email address"
              name="local_part"
              rules={[
                { required: true, message: "Required" },
                {
                  pattern: /^[a-z0-9][a-z0-9._+-]*$/i,
                  message: "Letters, digits, dot/underscore/plus/hyphen only",
                },
                { max: 64, message: "64 characters max" },
              ]}
              extra="The part before the @ symbol."
            >
              <Input
                placeholder="alice"
                autoComplete="off"
                addonAfter={`@${chosenDomain.name}`}
              />
            </Form.Item>

            <Form.Item
              label="Password"
              name="password"
              tooltip="Leave blank to auto-generate. Generated passwords are shown exactly once."
              rules={[{ min: 8, message: "8 characters minimum" }]}
            >
              <PasswordInput
                autoComplete="new-password"
                placeholder="(auto-generate)"
              />
            </Form.Item>

            <Form.Item
              label="Quota (MiB)"
              name="quota_mib"
              tooltip={`Default ${QUOTA_DEFAULT_BYTES / 1024 / 1024} MiB. Minimum ${
                QUOTA_MIN_BYTES / 1024 / 1024
              } MiB.`}
            >
              <InputNumber
                min={QUOTA_MIN_BYTES / 1024 / 1024}
                max={1024 * 1024}
                step={256}
                style={{ width: 200 }}
              />
            </Form.Item>
          </>
        )}

        {!chosenDomain && domains.length === 0 && (
          <Alert
            type="warning"
            showIcon
            message="No email-enabled domains"
            description="Enable email on at least one domain before creating a mailbox."
          />
        )}
      </Form>
    </Drawer>
  );
};
