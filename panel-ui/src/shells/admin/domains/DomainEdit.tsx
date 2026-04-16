import { useForm } from "@refinedev/antd";
import { Edit } from "@refinedev/antd";
import { Form, Input, Switch, Typography } from "antd";
import type { Domain } from "./DomainList";

export type DomainEditInput = {
  is_enabled?: boolean;
  nginx_custom_directives?: string;
};

export const DomainEdit = () => {
  const { formProps, saveButtonProps, queryResult } = useForm<DomainEditInput>({
    resource: "domains",
    action: "edit",
  });

  const domain = queryResult?.data?.data as Domain | undefined;

  return (
    <Edit saveButtonProps={saveButtonProps}>
      <Form {...formProps} layout="vertical">
        <Form.Item label="Name">
          <Typography.Text>{domain?.name}</Typography.Text>
        </Form.Item>

        <Form.Item label="Doc Root">
          <Typography.Text>{domain?.doc_root || "auto-generated"}</Typography.Text>
        </Form.Item>

        <Form.Item
          label="Enabled"
          name="is_enabled"
          valuePropName="checked"
        >
          <Switch />
        </Form.Item>

        <Form.Item
          label="Nginx Custom Directives"
          name="nginx_custom_directives"
          tooltip="Additional nginx configuration for this domain"
        >
          <Input.TextArea rows={6} />
        </Form.Item>
      </Form>
    </Edit>
  );
};
