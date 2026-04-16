import { useState } from "react";
import { Button, Modal, Checkbox, Space, message } from "antd";
import { DeleteOutlined } from "@ant-design/icons";
import { useInvalidate } from "@refinedev/core";
import { apiClient } from "../../../apiClient";

interface UserDeleteActionProps {
  recordItemId: string;
  userEmail: string;
}

export const UserDeleteAction = ({
  recordItemId,
  userEmail,
}: UserDeleteActionProps) => {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [purgeOS, setPurgeOS] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const invalidate = useInvalidate();

  const handleOpenModal = () => {
    setIsModalOpen(true);
  };

  const handleCancel = () => {
    setIsModalOpen(false);
    setPurgeOS(false);
  };

  const handleDelete = async () => {
    setIsLoading(true);
    try {
      const url = `/users/${encodeURIComponent(recordItemId)}${
        purgeOS ? "?purge=true" : ""
      }`;
      await apiClient.delete(url);

      message.success(
        purgeOS
          ? `User "${userEmail}" and OS account deleted`
          : `User "${userEmail}" deleted`
      );

      // Invalidate the users list to refresh the table
      invalidate({
        resource: "users",
        invalidates: ["list"],
      });

      setIsModalOpen(false);
      setPurgeOS(false);
    } catch (err: unknown) {
      const errMsg =
        err instanceof Error ? err.message : "Failed to delete user";
      message.error(errMsg);
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <>
      <Button
        danger
        size="small"
        icon={<DeleteOutlined />}
        onClick={handleOpenModal}
        style={{ padding: 0 }}
      />

      <Modal
        title={`Delete user "${userEmail}"?`}
        open={isModalOpen}
        onCancel={handleCancel}
        footer={[
          <Button key="cancel" onClick={handleCancel}>
            Cancel
          </Button>,
          <Button
            key="delete"
            danger
            type="primary"
            loading={isLoading}
            onClick={handleDelete}
          >
            Delete {purgeOS ? "and purge files" : "user"}
          </Button>,
        ]}
      >
        <Space direction="vertical" style={{ width: "100%" }}>
          <div>
            <p>
              <strong>Delete user:</strong> Removes the user record and all
              owned domains from the database. The OS account and home
              directory stay on disk.
            </p>
            <p>
              <strong>Delete and purge files:</strong> Removes the user record,
              all owned domains, AND deletes the OS account (
              <code>userdel --remove</code>) along with the <code>/home</code>{" "}
              directory. This is permanent and cannot be undone.
            </p>
          </div>
          <Checkbox
            checked={purgeOS}
            onChange={(e) => setPurgeOS(e.target.checked)}
          >
            Also delete OS account and /home/{userEmail.split("@")[0]} directory
            (destructive)
          </Checkbox>
        </Space>
      </Modal>
    </>
  );
};
