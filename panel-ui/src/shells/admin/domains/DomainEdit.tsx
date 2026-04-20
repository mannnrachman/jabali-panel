// DomainEdit — admin domain editor. Shows read-only name + doc_root,
// exposes the enabled toggle, nginx custom directives, and an SSL
// section. Post-M21: Form.useForm + useOneQuery + useUpdateMutation.
import { useEffect } from "react";
import {
  Button,
  Card,
  Divider,
  Form,
  Input,
  Spin,
  Switch,
  Typography,
  message,
} from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router";

import { useOneQuery, useUpdateMutation } from "../../../hooks/useQueries";
import type { Domain } from "./DomainList";
import { DomainSSLSection } from "./DomainSSLSection";

export type DomainEditInput = {
  is_enabled?: boolean;
  nginx_custom_directives?: string;
};

export const DomainEdit = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [form] = Form.useForm<DomainEditInput>();

  const { data: domain, isLoading } = useOneQuery<Domain>({
    resource: "domains",
    id,
  });
  const updateMutation = useUpdateMutation<Domain, DomainEditInput>({
    resource: "domains",
  });

  useEffect(() => {
    if (domain) {
      form.setFieldsValue({
        is_enabled: domain.is_enabled,
        nginx_custom_directives: domain.nginx_custom_directives,
      });
    }
  }, [domain, form]);

  const handleFinish = async (values: DomainEditInput) => {
    if (!id) return;
    try {
      await updateMutation.mutateAsync({ id, input: values });
      message.success("Domain updated");
      navigate("/jabali-admin/domains");
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to update domain";
      message.error(msg);
    }
  };

  if (isLoading && !domain) {
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
        Edit domain
      </Typography.Title>
      <Form<DomainEditInput>
        form={form}
        layout="vertical"
        onFinish={handleFinish}
      >
        <Form.Item label="Name">
          <Typography.Text>{domain?.name}</Typography.Text>
        </Form.Item>

        <Form.Item label="Doc Root">
          <Typography.Text>
            {domain?.doc_root || "auto-generated"}
          </Typography.Text>
        </Form.Item>

        <Form.Item label="Enabled" name="is_enabled" valuePropName="checked">
          <Switch />
        </Form.Item>

        <Form.Item
          label="Nginx Custom Directives"
          name="nginx_custom_directives"
          tooltip="Additional nginx configuration for this domain"
        >
          <Input.TextArea rows={6} />
        </Form.Item>

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

      {domain && (
        <>
          <Divider>SSL / HTTPS</Divider>
          <DomainSSLSection
            domainId={domain.id}
            sslEnabled={!!domain.ssl_enabled}
            onToggled={() =>
              // Refresh the ["one", "domains", id] cache so the
              // ssl_enabled badge + any downstream SSL state reflects
              // the toggle immediately.
              qc.invalidateQueries({ queryKey: ["one", "domains", id] })
            }
          />
        </>
      )}
    </Card>
  );
};
