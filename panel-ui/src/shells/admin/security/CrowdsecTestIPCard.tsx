// CrowdsecTestIPCard — "would this IP be blocked?" form. Folded into
// the CrowdSec tab from the deleted M43 Trust sub-tab. POSTs to
// /admin/security/trust/test (route preserved; renaming would break
// the endpoint without value).
import { Alert, Button, Card, Form, Input, Space, Table, Tag, Typography } from "antd";

import { useTrustTest, type TrustTestResponse } from "../../../hooks/useSecurityTrust";

export const CrowdsecTestIPCard = () => {
  const trustTest = useTrustTest();
  const [form] = Form.useForm<{ ip: string }>();
  const lastResult: TrustTestResponse | undefined = trustTest.data;
  const onTest = ({ ip }: { ip: string }) => trustTest.mutate(ip.trim());

  return (
    <Card size="small" title="Test IP — would this IP be blocked?">
      <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
        Asks every brain at once. Returns each layer's verdict so you can
        spot disagreement (the failure mode M43 fixes — see ADR-0089).
      </Typography.Paragraph>
      <Form form={form} layout="inline" onFinish={onTest} style={{ marginBottom: 16 }}>
        <Form.Item name="ip" rules={[{ required: true, message: "IP required" }]}>
          <Input placeholder="1.2.3.4" autoComplete="off" style={{ width: 220 }} />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" loading={trustTest.isPending}>
            Test
          </Button>
        </Form.Item>
      </Form>
      {trustTest.isError && (
        <Alert
          type="error"
          message="Test failed"
          description={trustTest.error instanceof Error ? trustTest.error.message : "unknown error"}
        />
      )}
      {lastResult && (
        <Table
          size="small"
          rowKey="layer"
          dataSource={lastResult.verdicts}
          pagination={false}
          columns={[
            { title: "Layer", dataIndex: "layer", width: 140 },
            {
              title: "Outcome",
              dataIndex: "outcome",
              width: 110,
              render: (o: string) => (
                <Tag color={o === "deny" ? "red" : o === "allow" ? "green" : "default"}>
                  {o}
                </Tag>
              ),
            },
            { title: "Detail", dataIndex: "detail" },
          ]}
          footer={() => (
            <Space>
              <Typography.Text type="secondary">IP tested:</Typography.Text>
              <Typography.Text code>{lastResult.ip}</Typography.Text>
            </Space>
          )}
        />
      )}
    </Card>
  );
};
